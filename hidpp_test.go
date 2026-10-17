package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestVoltageEvent(t *testing.T) {
	report := func(idx, fnsw byte, data ...byte) []byte {
		b := make([]byte, 20)
		b[0], b[1], b[2], b[3] = reportLong, deviceIdx, idx, fnsw
		copy(b[4:], data)
		return b
	}
	cases := []struct {
		name string
		b    []byte
		mv   uint16
		ok   bool
	}{
		// Captured from a G733: power-on, then power-off.
		{"on", report(8, 0x00, 0x0e, 0xc4, 0x01), 3780, true},
		{"off", report(8, 0x00, 0x00, 0x00, 0x00), 0, true},
		{"our response", report(8, 0x0b, 0x0e, 0xc4, 0x01), 0, false},
		{"other feature", report(5, 0x00, 0x02), 0, false},
		{"consumer key", []byte{0x02, 0x01}, 0, false},
	}
	for _, c := range cases {
		mv, ok := voltageEvent(c.b, 8)
		if mv != c.mv || ok != c.ok {
			t.Errorf("%s: got %d %v, want %d %v", c.name, mv, ok, c.mv, c.ok)
		}
	}
}

func TestHidrawNodes(t *testing.T) {
	sys := t.TempDir()
	add := func(node, uevent string) {
		dir := filepath.Join(sys, "class/hidraw", node, "device")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "uevent"), []byte(uevent), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	add("hidraw3", "DRIVER=hid-generic\nHID_ID=0003:00001B1C:00001BC4\nHID_NAME=Some Keyboard\n")
	add("hidraw10", "DRIVER=hid-generic\nHID_ID=0003:0000046D:00000AFE\nHID_NAME=Logitech G733 Gaming Headset\n")
	// Same vendor, product id that merely starts the same way.
	add("hidraw11", "HID_ID=0003:0000046D:00000AFE1\n")

	if got := hidrawNodes(sys, "046d:0afe"); !slices.Equal(got, []string{"/dev/hidraw10"}) {
		t.Errorf("nodes = %q", got)
	}
	if got := hidName(sys, "/dev/hidraw10"); got != "Logitech G733 Gaming Headset" {
		t.Errorf("name = %q", got)
	}
}
