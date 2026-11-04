// Package netscan finds likely receivers on a local network: it probes the
// FTP, Telnet and web ports of every address in a range, like the
// "Search IP address" tool of the original Dreambox Control Center.
package netscan

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// MaxAddresses bounds one scan (a /22).
	MaxAddresses = 1024
	probeTimeout = 700 * time.Millisecond
	bannerWait   = 400 * time.Millisecond
	workers      = 96
)

// Ports probed on every address.
var (
	PortFTP    = 21
	PortTelnet = 23
	PortHTTP   = 80
)

// Host is one address that answered on at least one management port.
type Host struct {
	IP     string `json:"ip"`
	FTP    bool   `json:"ftp"`
	Telnet bool   `json:"telnet"`
	HTTP   bool   `json:"http"`
	// Banner is the first line sent by the FTP service, when there was one.
	Banner string `json:"banner,omitempty"`
}

// Options customise a scan; the zero value probes the standard ports.
type Options struct {
	// Dial replaces net.Dialer for tests.
	Dial func(ctx context.Context, network, address string) (net.Conn, error)
	// Ports overrides the probed ports (FTP, Telnet, HTTP). Zero entries keep the defaults.
	FTP, Telnet, HTTP int
}

// ErrRange is returned for ranges that are malformed, too big or not local.
var ErrRange = errors.New("invalid address range")

// ParseRange accepts "192.168.1.0/24", "192.168.1.10-50" or
// "192.168.1.10-192.168.1.50" and returns the addresses to scan. Only
// private, loopback, link-local and carrier-grade-NAT IPv4 ranges are allowed:
// this tool is for finding devices on your own network, not for scanning the internet.
func ParseRange(text string) ([]netip.Addr, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, fmt.Errorf("%w: enter a range such as 192.168.1.0/24", ErrRange)
	}
	var first, last netip.Addr
	switch {
	case strings.Contains(text, "/"):
		prefix, err := netip.ParsePrefix(text)
		if err != nil || !prefix.Addr().Is4() {
			return nil, fmt.Errorf("%w: %q is not an IPv4 network", ErrRange, text)
		}
		prefix = prefix.Masked()
		if prefix.Bits() < 32-10 {
			return nil, fmt.Errorf("%w: at most %d addresses (a /22) per scan", ErrRange, MaxAddresses)
		}
		first = prefix.Addr()
		last = lastOf(prefix)
		// Skip the network and broadcast addresses of ordinary subnets.
		if prefix.Bits() <= 30 {
			first, last = first.Next(), last.Prev()
		}
	case strings.Contains(text, "-"):
		left, right, _ := strings.Cut(text, "-")
		var err error
		if first, err = netip.ParseAddr(strings.TrimSpace(left)); err != nil || !first.Is4() {
			return nil, fmt.Errorf("%w: %q is not an IPv4 address", ErrRange, left)
		}
		right = strings.TrimSpace(right)
		if n, convErr := strconv.Atoi(right); convErr == nil {
			if n < 0 || n > 255 {
				return nil, fmt.Errorf("%w: the last number must be 0-255", ErrRange)
			}
			octets := first.As4()
			octets[3] = byte(n)
			last = netip.AddrFrom4(octets)
		} else if last, err = netip.ParseAddr(right); err != nil || !last.Is4() {
			return nil, fmt.Errorf("%w: %q is not an IPv4 address", ErrRange, right)
		}
	default:
		addr, err := netip.ParseAddr(text)
		if err != nil || !addr.Is4() {
			return nil, fmt.Errorf("%w: %q is not an IPv4 address or range", ErrRange, text)
		}
		first, last = addr, addr
	}
	if first.Compare(last) > 0 {
		return nil, fmt.Errorf("%w: the range ends before it starts", ErrRange)
	}
	if !local(first) || !local(last) {
		return nil, fmt.Errorf("%w: only private network addresses (192.168.x.x, 10.x.x.x, 172.16-31.x.x) can be scanned", ErrRange)
	}
	var out []netip.Addr
	for addr := first; ; addr = addr.Next() {
		if !local(addr) {
			return nil, fmt.Errorf("%w: the range leaves the private address space", ErrRange)
		}
		out = append(out, addr)
		if len(out) > MaxAddresses {
			return nil, fmt.Errorf("%w: at most %d addresses per scan", ErrRange, MaxAddresses)
		}
		if addr == last {
			break
		}
	}
	return out, nil
}

func lastOf(prefix netip.Prefix) netip.Addr {
	octets := prefix.Addr().As4()
	value := uint32(octets[0])<<24 | uint32(octets[1])<<16 | uint32(octets[2])<<8 | uint32(octets[3])
	value |= ^uint32(0) >> uint(prefix.Bits())
	return netip.AddrFrom4([4]byte{byte(value >> 24), byte(value >> 16), byte(value >> 8), byte(value)})
}

func local(addr netip.Addr) bool {
	return addr.IsPrivate() || addr.IsLoopback() || addr.IsLinkLocalUnicast() ||
		netip.MustParsePrefix("100.64.0.0/10").Contains(addr)
}

// Scan probes every address and returns those with at least one open FTP
// or Telnet port, sorted by address.
func Scan(ctx context.Context, addrs []netip.Addr, opts Options) []Host {
	dial := opts.Dial
	if dial == nil {
		dialer := &net.Dialer{Timeout: probeTimeout}
		dial = dialer.DialContext
	}
	pick := func(value, fallback int) int {
		if value > 0 {
			return value
		}
		return fallback
	}
	ports := [3]int{pick(opts.FTP, PortFTP), pick(opts.Telnet, PortTelnet), pick(opts.HTTP, PortHTTP)}

	jobs := make(chan netip.Addr)
	var (
		mu    sync.Mutex
		found []Host
		wg    sync.WaitGroup
	)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for addr := range jobs {
				if host, ok := probe(ctx, dial, addr, ports); ok {
					mu.Lock()
					found = append(found, host)
					mu.Unlock()
				}
			}
		}()
	}
feed:
	for _, addr := range addrs {
		select {
		case jobs <- addr:
		case <-ctx.Done():
			break feed
		}
	}
	close(jobs)
	wg.Wait()
	sort.Slice(found, func(i, j int) bool {
		return netip.MustParseAddr(found[i].IP).Compare(netip.MustParseAddr(found[j].IP)) < 0
	})
	return found
}

func probe(ctx context.Context, dial func(context.Context, string, string) (net.Conn, error), addr netip.Addr, ports [3]int) (Host, bool) {
	host := Host{IP: addr.String()}
	check := func(port int, wantBanner bool) (bool, string) {
		if ctx.Err() != nil {
			return false, ""
		}
		probeCtx, cancel := context.WithTimeout(ctx, probeTimeout)
		defer cancel()
		conn, err := dial(probeCtx, "tcp", net.JoinHostPort(addr.String(), strconv.Itoa(port)))
		if err != nil {
			return false, ""
		}
		defer conn.Close()
		if !wantBanner {
			return true, ""
		}
		_ = conn.SetReadDeadline(time.Now().Add(bannerWait))
		line, _ := bufio.NewReaderSize(conn, 256).ReadString('\n')
		return true, cleanBanner(line)
	}
	var banner string
	var b string
	host.FTP, b = check(ports[0], true)
	banner = b
	host.Telnet, _ = check(ports[1], false)
	host.HTTP, _ = check(ports[2], false)
	host.Banner = banner
	return host, host.FTP || host.Telnet
}

func cleanBanner(line string) string {
	var out []rune
	for _, r := range strings.TrimSpace(line) {
		if r >= 0x20 && r != 0x7f {
			out = append(out, r)
		}
		if len(out) >= 80 {
			break
		}
	}
	return string(out)
}

// SuggestRanges lists /24 networks of this computer's private IPv4 interfaces.
func SuggestRanges() []string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		ip, ok := netip.AddrFromSlice(ipnet.IP.To4())
		if !ok || ip.IsLoopback() || !local(ip) {
			continue
		}
		octets := ip.As4()
		cidr := fmt.Sprintf("%d.%d.%d.0/24", octets[0], octets[1], octets[2])
		if !seen[cidr] {
			seen[cidr] = true
			out = append(out, cidr)
		}
	}
	sort.Strings(out)
	return out
}
