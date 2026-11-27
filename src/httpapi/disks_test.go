package httpapi

import (
	"os/exec"
	"strings"
	"testing"
)

const disksFixture = "@@mounts\nrootfs / rootfs rw 0 0\n/dev/mmcblk0p3 / ext4 rw,relatime 0 0\n/dev/sda1 /media/hdd ext4 rw,relatime 0 0\n" +
	"192.168.1.5:/export/movies /media/net/movies nfs rw,vers=3 0 0\n//192.168.1.7/My\\040Films /media/net/films cifs rw,vers=3.0 0 0\n" +
	"@@swaps\nFilename Type Size Used Priority\n@@df\nFilesystem 1024-blocks Used Available Capacity Mounted on\n" +
	"/dev/mmcblk0p3 3000000 1200000 1800000 41% /\n/dev/sda1 976000000 400000000 576000000 41% /media/hdd\n" +
	"192.168.1.5:/export/movies 2000000 500000 1500000 25% /media/net/movies\n" +
	"@@parts\nmajor minor  #blocks  name\n 179 0 7634944 mmcblk0\n 179 3 3145728 mmcblk0p3\n 8 0 976762584 sda\n 8 1 976760832 sda1\n 8 16 15000000 sdb\n 8 17 1 sdb1\n 8 18 14990000 sdb2\n 31 0 4 mtdblock0\n" +
	"@@blkid\n/dev/sda1: LABEL=\"hdd\" UUID=\"aaaa-bbbb\" TYPE=\"ext4\"\n/dev/sdb2: LABEL=\"USB\" UUID=\"1234-ABCD\" TYPE=\"vfat\"\n" +
	"@@sys\ndev=mmcblk0|0|\ndev=sda|0|WDC WD10EZEX   \ndev=sdb|1|USB DISK 2.0\n" +
	"@@mem\nMemTotal:  1000000 kB\nMemFree: 200000 kB\nBuffers: 10000 kB\nCached: 300000 kB\nSwapTotal: 0 kB\nSwapFree: 0 kB\n" +
	"@@fstab\n# comment\n/dev/sda1 /media/hdd ext4 defaults 0 0\n192.168.1.5:/export/movies /media/net/movies nfs nolock 0 0\n" +
	"@@fs\nnodev\tproc\n\text4\nnodev\tnfs\nnodev\tcifs\n@@tools\ntool=mkfs.ext4\ntool=mkfs.vfat\n@@fmt\nstate=done\ndev=/dev/sdb2\ncode=0\n@@log\nAll done\n@@end\n"

func TestParseDisksInfo(t *testing.T) {
	info := parseDisksInfo(disksFixture)
	if len(info.Disks) != 3 {
		t.Fatalf("want 3 disks, got %d: %+v", len(info.Disks), info.Disks)
	}
	byName := map[string]diskDevice{}
	for _, d := range info.Disks {
		byName[d.Name] = d
	}
	if !byName["mmcblk0"].System || !byName["mmcblk0"].Parts[0].System {
		t.Errorf("the disk holding / must be protected: %+v", byName["mmcblk0"])
	}
	sda := byName["sda"]
	if sda.System || sda.Model != "WDC WD10EZEX" || len(sda.Parts) != 1 {
		t.Fatalf("unexpected sda: %+v", sda)
	}
	p := sda.Parts[0]
	if p.Mount != "/media/hdd" || p.FS != "ext4" || p.Label != "hdd" || p.UsedKB != 400000000 || p.AvailKB != 576000000 {
		t.Errorf("unexpected sda1: %+v", p)
	}
	sdb := byName["sdb"]
	if !sdb.Removable || len(sdb.Parts) != 1 || sdb.Parts[0].Name != "sdb2" || sdb.Parts[0].Mount != "" {
		t.Errorf("extended partition must be skipped and sdb2 stay unmounted: %+v", sdb)
	}
	if len(info.Shares) != 2 || info.Shares[1].Source != "//192.168.1.7/My Films" || !info.Shares[0].Boot || info.Shares[1].Boot {
		t.Errorf("unexpected shares: %+v", info.Shares)
	}
	if info.Memory.TotalKB != 1000000 || info.Memory.AvailableKB != 510000 {
		t.Errorf("unexpected memory: %+v", info.Memory)
	}
	if strings.Join(info.Formats, ",") != "ext4,vfat" || !info.CIFS || !info.NFS {
		t.Errorf("unexpected capabilities: %+v %v %v", info.Formats, info.CIFS, info.NFS)
	}
	if info.Format.State != "done" || !info.Format.OK || info.Format.Device != "/dev/sdb2" || !strings.Contains(info.Format.Output, "All done") {
		t.Errorf("unexpected format state: %+v", info.Format)
	}
	if len(info.Fstab) != 2 || !info.Fstab[1].Network || !info.Fstab[1].Mounted {
		t.Errorf("unexpected fstab: %+v", info.Fstab)
	}
}

func allDiskCommands(t *testing.T) map[string]string {
	t.Helper()
	cmds := map[string]string{"info": disksInfoCommand}
	add := func(name string, cmd string, err error) {
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		cmds[name] = cmd
	}
	c, err := diskMountCommand("/dev/sda1", "/media/hdd", "auto")
	add("mount", c, err)
	c, err = diskMountCommand("/dev/sdb2", "/media/usb", "ntfs-3g")
	add("mount-typed", c, err)
	c, err = diskUnmountCommand("/media/hdd", false)
	add("umount", c, err)
	c, err = diskUnmountCommand("/media/hdd", true)
	add("umount-force", c, err)
	c, err = diskFstabMountCommand("/media/net/movies")
	add("fstab-mount", c, err)
	c, err = diskFstabRemoveCommand("/media/net/movies")
	add("fstab-remove", c, err)
	for _, fs := range []string{"ext4", "ext3", "vfat", "exfat", "ntfs"} {
		for _, label := range []string{"", "Movies"} {
			c, err = diskFormatStartCommand("/dev/sda1", fs, label)
			add("format-"+fs+"-"+label, c, err)
		}
	}
	c, err = diskShareCommand(diskRequest{Kind: "nfs", Host: "192.168.1.5", Share: "/export/movies", Mount: "/media/net/movies", Persist: true, Version: "3"})
	add("nfs-persist", c, err)
	c, err = diskShareCommand(diskRequest{Kind: "cifs", Host: "nas", Share: "My Films", Mount: "/media/net/films", Username: "me", Password: "it's a \"pass\" $x `y` \\n", Domain: "WORK"})
	add("cifs-user", c, err)
	c, err = diskShareCommand(diskRequest{Kind: "cifs", Host: "nas", Share: "Films", Mount: "/media/net/films2", Username: "me", Password: "p", Persist: true, Version: "2.1", Options: "iocharset=utf8"})
	add("cifs-persist", c, err)
	c, err = diskShareCommand(diskRequest{Kind: "cifs", Host: "nas", Share: "Public", Mount: "/media/net/public"})
	add("cifs-guest", c, err)
	return cmds
}

func TestDiskCommandsAreSingleLineAndShellValid(t *testing.T) {
	sh, lookErr := exec.LookPath("sh")
	for name, command := range allDiskCommands(t) {
		t.Run(name, func(t *testing.T) {
			if strings.ContainsAny(command, "\r\n\x00") {
				t.Fatalf("command contains a forbidden control character")
			}
			if len(command) > 5500 {
				t.Fatalf("command exceeds the Telnet input limit: %d bytes", len(command))
			}
			if lookErr == nil {
				if out, err := exec.Command(sh, "-n", "-c", command).CombinedOutput(); err != nil {
					t.Fatalf("invalid shell command: %v: %s\n%s", err, out, command)
				}
			}
		})
	}
}

func TestDiskCommandsRejectUnsafeInput(t *testing.T) {
	badMounts := []string{"/", "/etc", "/media", "/media/../etc", "/media/hdd; reboot", "/media/a b", "/mnt/", "media/hdd", "/media/$(id)", "/usr/bin"}
	for _, m := range badMounts {
		if _, err := diskUnmountCommand(m, false); err == nil {
			t.Errorf("unmount accepted %q", m)
		}
		if _, err := diskFstabRemoveCommand(m); err == nil {
			t.Errorf("fstab remove accepted %q", m)
		}
	}
	badDevices := []string{"/dev/sda", "/dev/sda1; reboot", "/dev/mtdblock3", "/dev/../sda1", "/dev/sda1 ", "sda1", "/dev/mmcblk0"}
	for _, d := range badDevices {
		if _, err := diskMountCommand(d, "/media/hdd", "auto"); err == nil {
			t.Errorf("mount accepted device %q", d)
		}
		if _, err := diskFormatStartCommand(d, "ext4", ""); err == nil {
			t.Errorf("format accepted device %q", d)
		}
	}
	if _, err := diskFormatStartCommand("/dev/sda1", "ext4", "a b;reboot"); err == nil {
		t.Error("format accepted a label with shell characters")
	}
	if _, err := diskFormatStartCommand("/dev/sda1", "reiserfs", ""); err == nil {
		t.Error("format accepted an unknown file system")
	}
	if _, err := diskMountCommand("/dev/sda1", "/media/hdd", "ext4 -o x"); err == nil {
		t.Error("mount accepted an unknown file system")
	}
	for name, req := range map[string]diskRequest{
		"host":      {Kind: "nfs", Host: "a;reboot", Share: "/x", Mount: "/media/net/x"},
		"nfs path":  {Kind: "nfs", Host: "a", Share: "/x'; reboot; '", Mount: "/media/net/x"},
		"nfs dots":  {Kind: "nfs", Host: "a", Share: "/x/../etc", Mount: "/media/net/x"},
		"cifs name": {Kind: "cifs", Host: "a", Share: "x$(id)", Mount: "/media/net/x"},
		"user":      {Kind: "cifs", Host: "a", Share: "x", Username: "me'; reboot", Mount: "/media/net/x"},
		"options":   {Kind: "cifs", Host: "a", Share: "x", Options: "rw; reboot", Mount: "/media/net/x"},
		"version":   {Kind: "cifs", Host: "a", Share: "x", Version: "9.9", Mount: "/media/net/x"},
		"kind":      {Kind: "sshfs", Host: "a", Share: "x", Mount: "/media/net/x"},
		"mount":     {Kind: "nfs", Host: "a", Share: "/x", Mount: "/etc"},
		"password":  {Kind: "cifs", Host: "a", Share: "x", Username: "me", Password: "bad\x07", Mount: "/media/net/x"},
	} {
		if _, err := diskShareCommand(req); err == nil {
			t.Errorf("share mount accepted bad %s", name)
		}
	}
}

func TestDiskShareCommandKeepsPasswordOutOfMountArguments(t *testing.T) {
	cmd, err := diskShareCommand(diskRequest{Kind: "cifs", Host: "nas", Share: "Films", Mount: "/media/net/films", Username: "me", Password: "s3cret"})
	if err != nil {
		t.Fatal(err)
	}
	mountPart := cmd[strings.Index(cmd, "mount -t cifs"):]
	if end := strings.Index(mountPart, "tail"); end > 0 {
		mountPart = mountPart[:end]
	}
	if strings.Contains(mountPart, "s3cret") || !strings.Contains(mountPart, "credentials=") {
		t.Errorf("password must travel in a credentials file, not on the mount command line: %s", mountPart)
	}
	if !strings.Contains(cmd, "rm -f /tmp/.dcc-cred.$$") {
		t.Errorf("the temporary credentials file must be removed: %s", cmd)
	}
	persist, err := diskShareCommand(diskRequest{Kind: "cifs", Host: "nas", Share: "Films", Mount: "/media/net/films", Username: "me", Password: "s3cret", Persist: true})
	if err != nil {
		t.Fatal(err)
	}
	fstabPart := persist[strings.Index(persist, "printf '%s\\n'"):]
	if strings.Contains(fstabPart, "s3cret") || !strings.Contains(fstabPart, "credentials=/etc/.dcc-cred-media_net_films") {
		t.Errorf("fstab line must reference a credentials file: %s", fstabPart)
	}
}

func TestParseDiskFormatStatus(t *testing.T) {
	done := parseDiskFormatStatus("state=done\ndev=/dev/sda1\ncode=0\n@@log\nCreating filesystem\r\nDone\n")
	if done.State != "done" || !done.OK || done.Device != "/dev/sda1" || !strings.Contains(done.Output, "Done") {
		t.Errorf("unexpected: %+v", done)
	}
	failed := parseDiskFormatStatus("state=done\ndev=/dev/sda1\ncode=1\n@@log\nmkfs.ext4: error\n")
	if failed.OK {
		t.Errorf("a non-zero exit code must not be reported as success: %+v", failed)
	}
	if st := parseDiskFormatStatus("state=running\ndev=/dev/x; reboot\n"); st.State != "running" || st.Device != "" {
		t.Errorf("unexpected: %+v", st)
	}
	if st := parseDiskFormatStatus("garbage"); st.State != "none" {
		t.Errorf("unexpected: %+v", st)
	}
}

func TestDiskStatusWords(t *testing.T) {
	cases := map[string]string{
		"MOUNTED\nSAVED\n":                      "SAVED",
		"MOUNTED\nFSTABFAILED\n":                "FSTABFAILED",
		"mount: wrong fs type\nFAILED\n":        "FAILED",
		"ISMOUNTED\n":                           "ISMOUNTED",
		"UNMOUNTED\n":                           "UNMOUNTED",
		"umount: busy\nSTILL\n":                 "STILL",
		"mount: noise MOUNTED but not a line\n": "",
	}
	for out, want := range cases {
		if got := diskStatusOf(out); got != want {
			t.Errorf("diskStatusOf(%q) = %q, want %q", out, got, want)
		}
	}
}
