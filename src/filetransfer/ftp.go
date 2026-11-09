package filetransfer

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/didou07/dcc-lite/src/core"
)

const (
	ftpControlTimeout = 15 * time.Second
	ftpDataTimeout    = 10 * time.Minute
	ftpNoopTimeout    = 8 * time.Second
	maxFTPReplyBytes  = 64 << 10
	maxFTPReplyLines  = 256
	maxFTPListingSize = 32 << 20
)

type FTPOptions struct {
	Host     string
	Port     int
	Username string
	Password string
}

type FTPClient struct {
	control net.Conn
	reader  *bufio.Reader
	host    string
	mu      sync.Mutex
	closed  bool
}

func OpenFTP(ctx context.Context, options FTPOptions) (*FTPClient, error) {
	if options.Host == "" || options.Port < 1 || options.Port > 65535 {
		return nil, errors.New("invalid FTP host or port")
	}
	options.Username = defaultFTPUsername(options.Username)
	if invalidFTPArgument(options.Username) || invalidFTPArgument(options.Password) {
		return nil, errors.New("FTP credentials contain unsupported control characters")
	}
	address := net.JoinHostPort(strings.Trim(options.Host, "[]"), strconv.Itoa(options.Port))
	conn, err := (&net.Dialer{Timeout: 8 * time.Second, KeepAlive: 30 * time.Second}).DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, fmt.Errorf("connect to FTP server: %w", err)
	}
	client := &FTPClient{control: conn, reader: bufio.NewReaderSize(conn, 32<<10), host: strings.Trim(options.Host, "[]")}
	deadline := time.Now().Add(ftpControlTimeout)
	if value, ok := ctx.Deadline(); ok && value.Before(deadline) {
		deadline = value
	}
	if err := conn.SetDeadline(deadline); err != nil {
		_ = conn.Close()
		return nil, err
	}
	code, _, err := client.readReply()
	if err == nil && code != 220 {
		err = fmt.Errorf("FTP server rejected connection (%d)", code)
	}
	if err == nil {
		code, _, err = client.command("USER", options.Username)
	}
	if err == nil && code == 331 {

		code, _, err = client.commandWithArgument("PASS", options.Password)
	}
	if err == nil && code != 230 {
		err = fmt.Errorf("FTP login failed (%d)", code)
	}
	if err == nil {
		code, _, err = client.command("TYPE", "I")
		if err == nil && code != 200 {
			err = fmt.Errorf("FTP server refused binary mode (%d)", code)
		}
	}
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	_ = conn.SetDeadline(time.Time{})
	return client, nil
}

func defaultFTPUsername(username string) string {
	if strings.TrimSpace(username) == "" {

		return "root"
	}
	return strings.TrimSpace(username)
}

func (c *FTPClient) List(ctx context.Context, remotePath string) ([]core.FileEntry, error) {
	if err := validateRemotePath(remotePath); err != nil {
		return nil, err
	}
	stop := c.beginContext(ctx, 30*time.Second)
	defer func() { stop(); _ = c.control.SetDeadline(time.Time{}) }()
	c.mu.Lock()
	defer c.mu.Unlock()
	data, err := c.dataCommand(ctx, "MLSD", remotePath)
	if err == nil {
		entries := parseMLSD(data, remotePath)
		return entries, nil
	}
	if !isUnsupportedFTPCommand(err) {
		return nil, err
	}
	data, err = c.dataCommand(ctx, "LIST", remotePath)
	if err != nil {
		return nil, err
	}
	return parseFTPList(data, remotePath), nil
}

func (c *FTPClient) Upload(ctx context.Context, localPath, remotePath string, progress func(core.TransferProgress)) error {
	if err := validateRemotePath(remotePath); err != nil {
		return err
	}
	file, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("open local upload file: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("upload source must be a regular file")
	}
	stop := c.beginContext(ctx, ftpDataTimeout)
	defer func() { stop(); _ = c.control.SetDeadline(time.Time{}) }()
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.transfer(ctx, "STOR", remotePath, file, io.Discard, info.Size(), progress)
}

func (c *FTPClient) Download(ctx context.Context, remotePath, localPath string, progress func(core.TransferProgress)) error {
	if err := validateRemotePath(remotePath); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(localPath), 0o700); err != nil {
		return fmt.Errorf("create local download directory: %w", err)
	}
	if _, err := os.Lstat(localPath); err == nil {
		return errors.New("local destination already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(localPath), ".dccnext-download-*.part")
	if err != nil {
		return fmt.Errorf("create temporary download file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	stop := c.beginContext(ctx, ftpDataTimeout)
	defer func() { stop(); _ = c.control.SetDeadline(time.Time{}) }()
	c.mu.Lock()
	err = c.transfer(ctx, "RETR", remotePath, nil, tmp, -1, progress)
	c.mu.Unlock()
	if err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, localPath); err != nil {
		return fmt.Errorf("commit downloaded file: %w", err)
	}
	return nil
}

func (c *FTPClient) DownloadPartial(ctx context.Context, remotePath, partialPath string, progress func(core.TransferProgress)) error {
	if err := validateRemotePath(remotePath); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(partialPath), 0o700); err != nil {
		return fmt.Errorf("create local download directory: %w", err)
	}
	file, err := os.OpenFile(partialPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create partial download: %w", err)
	}
	defer file.Close()
	stop := c.beginContext(ctx, ftpDataTimeout)
	defer func() { stop(); _ = c.control.SetDeadline(time.Time{}) }()
	c.mu.Lock()
	err = c.transferAtOffset(ctx, "RETR", remotePath, nil, file, -1, 0, progress)
	c.mu.Unlock()
	if syncErr := file.Sync(); err == nil && syncErr != nil {
		err = syncErr
	}
	if err != nil {
		return fmt.Errorf("FTP partial download: %w", err)
	}
	return nil
}

func (c *FTPClient) ResumeDownload(ctx context.Context, remotePath, partialPath string, progress func(core.TransferProgress)) error {
	if err := validateRemotePath(remotePath); err != nil {
		return err
	}
	partialInfo, statErr := os.Lstat(partialPath)
	if errors.Is(statErr, os.ErrNotExist) {
		return c.DownloadPartial(ctx, remotePath, partialPath, progress)
	} else if statErr != nil {
		return statErr
	} else if partialInfo.Mode()&os.ModeSymlink != 0 || !partialInfo.Mode().IsRegular() {
		return errors.New("partial download must be a regular file, not a symbolic link or special file")
	}
	file, err := os.OpenFile(partialPath, os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open partial download: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("partial download is not a regular file")
	}
	offset := info.Size()
	stop := c.beginContext(ctx, ftpDataTimeout)
	defer func() { stop(); _ = c.control.SetDeadline(time.Time{}) }()
	c.mu.Lock()
	total, sizeErr := c.remoteSizeLocked(remotePath)
	if sizeErr == nil {
		switch {
		case offset > total:
			err = fmt.Errorf("local partial size (%d) exceeds remote file size (%d)", offset, total)
		case offset == total:
			if progress != nil {
				progress(core.TransferProgress{BytesDone: total, BytesTotal: total, Fraction: 1})
			}
		default:
			err = c.transferAtOffset(ctx, "RETR", remotePath, nil, file, total, offset, progress)
		}
	} else {
		err = fmt.Errorf("cannot safely resume download: %w", sizeErr)
	}
	c.mu.Unlock()
	if syncErr := file.Sync(); err == nil && syncErr != nil {
		err = syncErr
	}
	if err != nil {
		return fmt.Errorf("FTP resume download: %w", err)
	}
	return nil
}

func (c *FTPClient) ResumeUpload(ctx context.Context, localPath, remotePath string, progress func(core.TransferProgress)) error {
	if err := validateRemotePath(remotePath); err != nil {
		return err
	}
	file, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("upload source must be a regular file")
	}
	stop := c.beginContext(ctx, ftpDataTimeout)
	defer func() { stop(); _ = c.control.SetDeadline(time.Time{}) }()
	c.mu.Lock()
	offset, sizeErr := c.remoteSizeLocked(remotePath)
	if sizeErr != nil && !isFTPPathNotFound(sizeErr) {
		c.mu.Unlock()
		return fmt.Errorf("cannot safely resume upload: %w", sizeErr)
	}
	if sizeErr != nil {
		offset = 0
	}
	if offset > info.Size() {
		c.mu.Unlock()
		return fmt.Errorf("remote partial size (%d) exceeds local source size (%d)", offset, info.Size())
	}
	if offset == info.Size() {
		c.mu.Unlock()
		if progress != nil {
			progress(core.TransferProgress{BytesDone: info.Size(), BytesTotal: info.Size(), Fraction: 1})
		}
		return nil
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		c.mu.Unlock()
		return err
	}
	err = c.transferAtOffset(ctx, "STOR", remotePath, file, io.Discard, info.Size(), offset, progress)
	c.mu.Unlock()
	if err != nil {
		return fmt.Errorf("FTP resume upload: %w", err)
	}
	return nil
}

func isFTPPathNotFound(err error) bool {
	return err != nil && strings.Contains(err.Error(), "FTP SIZE rejected (550)")
}

func (c *FTPClient) remoteSizeLocked(remotePath string) (int64, error) {
	code, message, err := c.command("SIZE", remotePath)
	if err != nil {
		return 0, err
	}
	if code != 213 {
		return 0, fmt.Errorf("FTP SIZE rejected (%d)", code)
	}
	fields := strings.Fields(message)
	if len(fields) == 0 {
		return 0, errors.New("FTP SIZE returned an invalid response")
	}
	value, err := strconv.ParseInt(fields[len(fields)-1], 10, 64)
	if err != nil || value < 0 {
		return 0, errors.New("FTP SIZE returned an invalid file length")
	}
	return value, nil
}

func (c *FTPClient) Mkdir(ctx context.Context, remotePath string) error {
	return c.simplePathCommand(ctx, "MKD", remotePath, 257, 250)
}

func (c *FTPClient) Rename(ctx context.Context, oldPath, newPath string) error {
	if err := validateRemotePath(oldPath); err != nil {
		return err
	}
	if err := validateRemotePath(newPath); err != nil {
		return err
	}
	stop := c.beginContext(ctx, ftpControlTimeout)
	defer func() { stop(); _ = c.control.SetDeadline(time.Time{}) }()
	c.mu.Lock()
	defer c.mu.Unlock()
	code, _, err := c.command("RNFR", oldPath)
	if err != nil {
		return err
	}
	if code != 350 {
		return fmt.Errorf("FTP rename source rejected (%d)", code)
	}
	code, _, err = c.command("RNTO", newPath)
	if err != nil {
		return err
	}
	if code != 250 {
		return fmt.Errorf("FTP rename destination rejected (%d)", code)
	}
	return nil
}

func (c *FTPClient) Remove(ctx context.Context, remotePath string, directory bool) error {
	command := "DELE"
	if directory {
		command = "RMD"
	}
	return c.simplePathCommand(ctx, command, remotePath, 250, 257)
}

func (c *FTPClient) Chmod(ctx context.Context, remotePath string, mode uint32) error {
	if err := validateRemotePath(remotePath); err != nil {
		return err
	}
	if mode > 0o7777 {
		return errors.New("invalid file mode")
	}
	stop := c.beginContext(ctx, ftpControlTimeout)
	defer func() { stop(); _ = c.control.SetDeadline(time.Time{}) }()
	c.mu.Lock()
	defer c.mu.Unlock()
	code, _, err := c.command("SITE", fmt.Sprintf("CHMOD %04o %s", mode, remotePath))
	if err != nil {
		return err
	}
	if code != 200 && code != 250 {
		return fmt.Errorf("FTP server does not support CHMOD (%d)", code)
	}
	return nil
}

func (c *FTPClient) SetModTime(ctx context.Context, remotePath string, modified time.Time) error {
	if err := validateRemotePath(remotePath); err != nil {
		return err
	}
	if modified.IsZero() {
		return nil
	}
	stop := c.beginContext(ctx, ftpControlTimeout)
	defer func() { stop(); _ = c.control.SetDeadline(time.Time{}) }()
	c.mu.Lock()
	defer c.mu.Unlock()
	argument := modified.UTC().Format("20060102150405") + " " + remotePath
	code, _, err := c.command("MFMT", argument)
	if err != nil {
		return err
	}
	if code == 213 {
		return nil
	}
	if code == 500 || code == 502 || code == 504 {
		return core.ErrMetadataUnsupported
	}
	return fmt.Errorf("FTP server could not preserve modification time (%d)", code)
}

func (c *FTPClient) simplePathCommand(ctx context.Context, command, remotePath string, accepted ...int) error {
	if err := validateRemotePath(remotePath); err != nil {
		return err
	}
	stop := c.beginContext(ctx, ftpControlTimeout)
	defer func() { stop(); _ = c.control.SetDeadline(time.Time{}) }()
	c.mu.Lock()
	defer c.mu.Unlock()
	code, _, err := c.command(command, remotePath)
	if err != nil {
		return err
	}
	for _, value := range accepted {
		if code == value {
			return nil
		}
	}
	return fmt.Errorf("FTP %s failed (%d)", command, code)
}

func (c *FTPClient) transfer(ctx context.Context, command, remotePath string, source io.Reader, destination io.Writer, total int64, progress func(core.TransferProgress)) error {
	return c.transferAtOffset(ctx, command, remotePath, source, destination, total, 0, progress)
}

func (c *FTPClient) transferAtOffset(ctx context.Context, command, remotePath string, source io.Reader, destination io.Writer, total, offset int64, progress func(core.TransferProgress)) error {
	dataConn, err := c.openPassive(ctx)
	if err != nil {
		return err
	}
	if offset > 0 {
		code, _, restErr := c.command("REST", strconv.FormatInt(offset, 10))
		if restErr != nil || code != 350 {
			_ = dataConn.Close()
			if restErr != nil {
				return fmt.Errorf("FTP REST failed: %w", restErr)
			}
			return fmt.Errorf("FTP server refused restart offset (%d)", code)
		}
	}
	code, _, err := c.command(command, remotePath)
	if err != nil {
		_ = dataConn.Close()
		return err
	}
	if code != 125 && code != 150 {
		_ = dataConn.Close()
		return fmt.Errorf("FTP %s rejected (%d)", command, code)
	}
	stopData := setConnContext(dataConn, ctx, ftpDataTimeout)
	var copied int64
	var copyErr error
	if source != nil {
		copied, copyErr = io.Copy(dataConn, &progressReader{reader: source, total: total, done: offset, callback: progress})
	} else {
		copied, copyErr = io.Copy(&progressWriter{writer: destination, total: total, done: offset, callback: progress}, dataConn)
	}
	closeErr := dataConn.Close()
	stopData()
	if copyErr != nil {
		c.drainTransferReplies()
		return fmt.Errorf("FTP transfer failed: %w", copyErr)
	}
	if closeErr != nil {
		c.drainTransferReplies()
		return fmt.Errorf("close FTP data connection: %w", closeErr)
	}
	code, _, err = c.readReply()
	if err != nil {
		return err
	}
	if code != 226 && code != 250 {
		return fmt.Errorf("FTP transfer did not complete (%d)", code)
	}
	if total >= 0 && offset+copied != total {
		return fmt.Errorf("FTP transfer length mismatch: received %d of %d bytes", offset+copied, total)
	}
	if progress != nil {
		progress(core.TransferProgress{BytesDone: offset + copied, BytesTotal: total, Fraction: fraction(offset+copied, total)})
	}
	return nil
}

func (c *FTPClient) drainTransferReplies() {
	_ = c.control.SetDeadline(time.Now().Add(1200 * time.Millisecond))
	defer c.control.SetDeadline(time.Time{})
	for i := 0; i < 2; i++ {
		code, _, err := c.readReply()
		if err != nil || code >= 200 && code < 400 {
			return
		}
	}
}

type progressReader struct {
	reader   io.Reader
	total    int64
	done     int64
	callback func(core.TransferProgress)
}

func (r *progressReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	r.done += int64(n)
	if n > 0 && r.callback != nil {
		r.callback(core.TransferProgress{BytesDone: r.done, BytesTotal: r.total, Fraction: fraction(r.done, r.total)})
	}
	return n, err
}

type progressWriter struct {
	writer   io.Writer
	total    int64
	done     int64
	callback func(core.TransferProgress)
}

func (w *progressWriter) Write(p []byte) (int, error) {
	n, err := w.writer.Write(p)
	w.done += int64(n)
	if n > 0 && w.callback != nil {
		w.callback(core.TransferProgress{BytesDone: w.done, BytesTotal: w.total, Fraction: fraction(w.done, w.total)})
	}
	return n, err
}
func fraction(done, total int64) float64 {
	if total <= 0 {
		return 0
	}
	f := float64(done) / float64(total)
	if f > 1 {
		return 1
	}
	return f
}

func (c *FTPClient) dataCommand(ctx context.Context, command, remotePath string) ([]byte, error) {
	dataConn, err := c.openPassive(ctx)
	if err != nil {
		return nil, err
	}
	argument := ""
	if remotePath != "" && remotePath != "." {
		argument = remotePath
	}
	code, _, err := c.command(command, argument)
	if err != nil {
		_ = dataConn.Close()
		return nil, err
	}
	if code != 125 && code != 150 {
		_ = dataConn.Close()
		return nil, fmt.Errorf("FTP %s rejected (%d)", command, code)
	}
	stopData := setConnContext(dataConn, ctx, ftpDataTimeout)
	data, readErr := readBounded(dataConn, maxFTPListingSize)
	closeErr := dataConn.Close()
	stopData()

	code, _, replyErr := c.readReply()
	if replyErr != nil {
		return nil, errors.Join(readErr, closeErr, replyErr)
	}
	if code != 226 && code != 250 {
		return nil, errors.Join(readErr, closeErr, fmt.Errorf("FTP %s did not complete (%d)", command, code))
	}
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	return data, nil
}

func readBounded(reader io.Reader, limit int64) ([]byte, error) {
	if limit < 0 {
		return nil, errors.New("invalid read limit")
	}
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("FTP directory listing exceeds the %d-byte safety limit", limit)
	}
	return data, nil
}

func (c *FTPClient) openPassive(ctx context.Context) (net.Conn, error) {
	var epsvErr error
	code, response, err := c.command("EPSV", "")
	if err != nil {
		epsvErr = err
	} else if code == 229 {
		port, parseErr := parseEPSVPort(response)
		if parseErr != nil {
			epsvErr = parseErr
		} else {
			conn, dialErr := (&net.Dialer{Timeout: 8 * time.Second}).DialContext(ctx, "tcp", net.JoinHostPort(c.host, strconv.Itoa(port)))
			if dialErr == nil {
				return conn, nil
			}
			epsvErr = fmt.Errorf("EPSV data connection: %w", dialErr)
		}
	} else {
		epsvErr = fmt.Errorf("EPSV not supported (%d)", code)
	}

	code, response, err = c.command("PASV", "")
	if err != nil {
		if epsvErr != nil {
			return nil, fmt.Errorf("FTP passive negotiation failed (EPSV: %v; PASV: %w)", epsvErr, err)
		}
		return nil, err
	}
	if code != 227 {
		return nil, fmt.Errorf("FTP passive mode unavailable (EPSV: %v; PASV: %d)", epsvErr, code)
	}
	port, parseErr := parsePASVPort(response)
	if parseErr != nil {
		return nil, fmt.Errorf("invalid PASV response after EPSV fallback (%v): %w", epsvErr, parseErr)
	}

	conn, dialErr := (&net.Dialer{Timeout: 8 * time.Second}).DialContext(ctx, "tcp", net.JoinHostPort(c.host, strconv.Itoa(port)))
	if dialErr != nil {
		return nil, fmt.Errorf("FTP PASV data connection failed (EPSV: %v): %w", epsvErr, dialErr)
	}
	return conn, nil
}

func parseEPSVPort(message string) (int, error) {
	start := strings.LastIndex(message, "(")
	end := strings.LastIndex(message, ")")
	if start < 0 || end <= start {
		return 0, errors.New("invalid FTP EPSV response")
	}
	value := message[start+1 : end]
	if len(value) < 5 {
		return 0, errors.New("invalid FTP EPSV port")
	}
	delimiter := value[0]
	parts := strings.Split(value, string(delimiter))
	if len(parts) < 5 {
		return 0, errors.New("invalid FTP EPSV port")
	}
	port, err := strconv.Atoi(parts[3])
	if err != nil || port < 1 || port > 65535 {
		return 0, errors.New("invalid FTP EPSV port")
	}
	return port, nil
}

func parsePASVPort(message string) (int, error) {
	start := strings.LastIndex(message, "(")
	end := strings.LastIndex(message, ")")
	if start < 0 || end <= start {
		return 0, errors.New("invalid FTP PASV response")
	}
	parts := strings.Split(message[start+1:end], ",")
	if len(parts) != 6 {
		return 0, errors.New("invalid FTP PASV response")
	}
	p1, e1 := strconv.Atoi(strings.TrimSpace(parts[4]))
	p2, e2 := strconv.Atoi(strings.TrimSpace(parts[5]))
	if e1 != nil || e2 != nil || p1 < 0 || p1 > 255 || p2 < 0 || p2 > 255 {
		return 0, errors.New("invalid FTP PASV port")
	}
	port := p1*256 + p2
	if port == 0 {
		return 0, errors.New("invalid FTP PASV port")
	}
	return port, nil
}

func (c *FTPClient) command(command, argument string) (int, string, error) {
	return c.writeCommand(command, argument, argument != "")
}

func (c *FTPClient) commandWithArgument(command, argument string) (int, string, error) {
	return c.writeCommand(command, argument, true)
}

func (c *FTPClient) writeCommand(command, argument string, includeSeparator bool) (int, string, error) {
	if c.closed {
		return 0, "", net.ErrClosed
	}
	if command == "" || strings.ContainsAny(command, " \t\r\n\x00") || invalidFTPArgument(argument) {
		return 0, "", errors.New("FTP command or argument contains unsupported characters")
	}
	line := command
	if includeSeparator {
		line += " " + argument
	}
	if _, err := io.WriteString(c.control, line+"\r\n"); err != nil {
		return 0, "", c.failControl(fmt.Errorf("write FTP command: %w", err))
	}
	return c.readReply()
}

func (c *FTPClient) readReply() (int, string, error) {
	line, err := c.readLine()
	if err != nil {
		return 0, "", err
	}
	if len(line) < 3 {
		return 0, line, c.failControl(errors.New("invalid FTP response"))
	}
	code, err := strconv.Atoi(line[:3])
	if err != nil {
		return 0, line, c.failControl(errors.New("invalid FTP response code"))
	}
	lines := []string{line}
	totalBytes := len(line) + 2
	if len(line) > 3 && line[3] == '-' {
		for len(lines) < maxFTPReplyLines {
			next, e := c.readLine()
			if e != nil {
				return 0, strings.Join(lines, "\n"), e
			}
			totalBytes += len(next) + 2
			if totalBytes > maxFTPReplyBytes {
				return 0, "", errors.New("FTP multiline reply exceeds size limit")
			}
			lines = append(lines, next)
			if strings.HasPrefix(next, strconv.Itoa(code)+" ") {
				return code, strings.Join(lines, "\n"), nil
			}
		}
		return 0, "", c.failControl(errors.New("FTP multiline reply has no terminating status line"))
	}
	return code, strings.Join(lines, "\n"), nil
}
func (c *FTPClient) readLine() (string, error) {
	line := make([]byte, 0, min(c.reader.Size(), maxFTPReplyBytes))
	for {
		part, err := c.reader.ReadSlice('\n')
		if len(line)+len(part) > maxFTPReplyBytes {
			return "", c.failControl(errors.New("FTP reply line exceeds size limit"))
		}
		line = append(line, part...)
		if err == bufio.ErrBufferFull {
			continue
		}
		if err != nil {

			var netErr net.Error
			if !(errors.As(err, &netErr) && netErr.Timeout()) {
				return "", c.failControl(err)
			}
			return "", err
		}
		return strings.TrimRight(string(line), "\r\n"), nil
	}
}

func (c *FTPClient) failControl(err error) error {
	if !c.closed {
		c.closed = true
		if c.control != nil {
			_ = c.control.Close()
		}
	}
	return err
}

func (c *FTPClient) beginContext(ctx context.Context, d time.Duration) func() bool {
	deadline := time.Now().Add(d)
	if value, ok := ctx.Deadline(); ok && value.Before(deadline) {
		deadline = value
	}
	_ = c.control.SetDeadline(deadline)
	return context.AfterFunc(ctx, func() { _ = c.control.SetDeadline(time.Now()) })
}
func setConnContext(conn net.Conn, ctx context.Context, d time.Duration) func() bool {
	deadline := time.Now().Add(d)
	if value, ok := ctx.Deadline(); ok && value.Before(deadline) {
		deadline = value
	}
	_ = conn.SetDeadline(deadline)
	return context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Now()) })
}

func (c *FTPClient) IsClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

func (c *FTPClient) Noop(ctx context.Context) error {
	stop := c.beginContext(ctx, ftpNoopTimeout)
	defer func() { stop(); _ = c.control.SetDeadline(time.Time{}) }()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return net.ErrClosed
	}
	_, _, err := c.command("NOOP", "")
	if err != nil {

		_ = c.failControl(err)
	}
	return err
}

func (c *FTPClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	_ = c.control.SetDeadline(time.Now().Add(time.Second))
	_, _, _ = c.command("QUIT", "")
	c.closed = true
	return c.control.Close()
}

func invalidFTPArgument(value string) bool {
	return strings.ContainsAny(value, "\r\n\x00") || strings.IndexFunc(value, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0
}
func validateRemotePath(value string) error {
	if len(value) > 4096 || value == "" || invalidFTPArgument(value) {
		return errors.New("remote path is empty, too long or contains control characters")
	}
	return nil
}
func isUnsupportedFTPCommand(err error) bool {
	return err != nil && (strings.Contains(err.Error(), "(500)") || strings.Contains(err.Error(), "(502)") || strings.Contains(err.Error(), "(504)"))
}

func parseMLSD(data []byte, directory string) []core.FileEntry {
	var entries []core.FileEntry
	for _, line := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		separator := strings.Index(line, "; ")
		if separator < 0 {
			continue
		}
		facts := make(map[string]string)
		for _, fact := range strings.Split(line[:separator+1], ";") {
			key, value, ok := strings.Cut(fact, "=")
			if ok {
				facts[strings.ToLower(key)] = value
			}
		}
		name := line[separator+2:]
		if name == "" || name == "." || name == ".." || invalidFTPArgument(name) {
			continue
		}
		typ := strings.ToLower(facts["type"])
		if typ == "cdir" || typ == "pdir" {
			continue
		}
		entry := core.FileEntry{Name: name, Path: path.Join(directory, name), IsDir: typ == "dir", Mode: facts["unix.mode"]}
		entry.Size, _ = strconv.ParseInt(facts["size"], 10, 64)
		if modified := facts["modify"]; len(modified) >= 14 {
			if parsed, e := time.Parse("20060102150405", modified[:14]); e == nil {
				entry.ModifiedAt = parsed.UTC()
			}
		}
		entries = append(entries, entry)
	}
	return entries
}

func parseFTPList(data []byte, directory string) []core.FileEntry {
	var entries []core.FileEntry
	for _, line := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 9 || len(fields[0]) < 10 {
			continue
		}
		mode := fields[0]
		name := strings.Join(fields[8:], " ")
		if strings.HasPrefix(mode, "l") {
			if target := strings.Index(name, " -> "); target >= 0 {
				name = name[:target]
			}
		}
		if name == "" || name == "." || name == ".." || invalidFTPArgument(name) {
			continue
		}
		size, _ := strconv.ParseInt(fields[4], 10, 64)
		entry := core.FileEntry{Name: name, Path: path.Join(directory, name), Size: size, IsDir: strings.HasPrefix(mode, "d"), Mode: mode}
		stamp := strings.Join(fields[5:8], " ")
		for _, layout := range []string{"Jan _2 15:04", "Jan _2 2006"} {
			if parsed, e := time.Parse(layout, stamp); e == nil {
				entry.ModifiedAt = parsed
				break
			}
		}
		entries = append(entries, entry)
	}
	return entries
}

func cleanRemoteJoin(directory, name string) string {
	if strings.HasPrefix(name, "/") {
		return path.Clean(name)
	}
	return path.Clean(path.Join(directory, name))
}
