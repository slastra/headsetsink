package main

import (
	"os"
	"slices"
	"testing"
)

func loadNodes(t *testing.T, file string) []audioNode {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := parseNodes(data)
	if err != nil {
		t.Fatal(err)
	}
	return nodes
}

const g733 = "046d:0afe"

func TestHeadsetNode(t *testing.T) {
	sinks := loadNodes(t, "testdata/sinks.json")
	if got, want := headsetNode(sinks, g733), "alsa_output.usb-Logitech_G733_Gaming_Headset_0000000000000000-00.analog-stereo"; got != want {
		t.Errorf("sink = %q, want %q", got, want)
	}
	// The sink's monitor has the same USB id and comes first; it must be skipped.
	sources := loadNodes(t, "testdata/sources.json")
	if got, want := headsetNode(sources, g733), "alsa_input.usb-Logitech_G733_Gaming_Headset_0000000000000000-00.mono-fallback"; got != want {
		t.Errorf("source = %q, want %q", got, want)
	}
	if got := headsetNode(sinks, "046d:c539"); got != "" {
		t.Errorf("unknown device matched %q", got)
	}
}

func TestUSBID(t *testing.T) {
	sinks := loadNodes(t, "testdata/sinks.json")
	var ids []string
	for _, n := range sinks {
		ids = append(ids, n.usbID())
	}
	// PCI devices have vendor ids too, but aren't USB.
	want := []string{"", "", "0c76:161f", g733}
	if !slices.Equal(ids, want) {
		t.Errorf("ids = %q, want %q", ids, want)
	}
}

func TestCandidates(t *testing.T) {
	sinks := loadNodes(t, "testdata/sinks.json")
	if got := candidates(sinks); !slices.Equal(got, []string{g733}) {
		t.Errorf("candidates = %q", got)
	}
	if got := candidates(append(sinks, sinks...)); len(got) != 1 {
		t.Errorf("duplicates not collapsed: %q", got)
	}
}

func TestMemory(t *testing.T) {
	m := memory{t.TempDir() + "/headsetsink"}
	if got := m.load("sink"); got != "" {
		t.Errorf("empty memory loaded %q", got)
	}
	m.save("sink", "speakers")
	m.save("source", "camera")
	if got := m.load("sink"); got != "speakers" {
		t.Errorf("sink = %q", got)
	}
	if got := m.load("source"); got != "camera" {
		t.Errorf("source = %q", got)
	}
	m.save("sink", "")
	if got := m.load("sink"); got != "speakers" {
		t.Errorf("empty save overwrote: %q", got)
	}
}
