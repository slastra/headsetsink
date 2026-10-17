package main

// The PipeWire side, through pactl (pipewire-pulse): find the headset's sink
// and source by USB id, read and set defaults, and remember what the headset
// replaced.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type audioNode struct {
	Name       string            `json:"name"`
	Properties map[string]string `json:"properties"`
}

// usbID returns "vvvv:pppp" for a USB device's node, or "" for anything else.
func (n audioNode) usbID() string {
	if n.Properties["device.bus"] != "usb" {
		return ""
	}
	v := strings.TrimPrefix(n.Properties["device.vendor.id"], "0x")
	p := strings.TrimPrefix(n.Properties["device.product.id"], "0x")
	if v == "" || p == "" {
		return ""
	}
	return strings.ToLower(v + ":" + p)
}

// Every sink has a monitor source with the same device properties.
func (n audioNode) isMonitor() bool { return n.Properties["device.class"] == "monitor" }

func parseNodes(data []byte) ([]audioNode, error) {
	var nodes []audioNode
	err := json.Unmarshal(data, &nodes)
	return nodes, err
}

// listNodes lists sinks or sources ("sink" or "source").
func listNodes(kind string) ([]audioNode, error) {
	out, err := exec.Command("pactl", "-f", "json", "list", kind+"s").Output()
	if err != nil {
		return nil, fmt.Errorf("pactl list %ss: %w", kind, err)
	}
	return parseNodes(out)
}

// headsetNode returns the name of the first non-monitor node of USB device id.
func headsetNode(nodes []audioNode, id string) string {
	for _, n := range nodes {
		if !n.isMonitor() && n.usbID() == id {
			return n.Name
		}
	}
	return ""
}

func lookup(nodes []audioNode, name string) (audioNode, bool) {
	for _, n := range nodes {
		if n.Name == name {
			return n, true
		}
	}
	return audioNode{}, false
}

// candidates lists the USB ids of Logitech devices that have a sink, which is
// where a headset dongle shows up.
func candidates(sinks []audioNode) []string {
	var ids []string
	for _, n := range sinks {
		id := n.usbID()
		if strings.HasPrefix(id, "046d:") && !contains(ids, id) {
			ids = append(ids, id)
		}
	}
	return ids
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func getDefault(kind string) string {
	out, err := exec.Command("pactl", "get-default-"+kind).Output()
	if err != nil {
		log.Printf("pactl get-default-%s: %v", kind, err)
	}
	return strings.TrimSpace(string(out))
}

func setDefault(kind, name string) {
	if out, err := exec.Command("pactl", "set-default-"+kind, name).CombinedOutput(); err != nil {
		log.Printf("pactl set-default-%s %s: %v %s", kind, name, err, out)
		return
	}
	log.Printf("default %s -> %s", kind, name)
}

// memory keeps the last default sink and source that weren't the headset,
// in files under $XDG_STATE_HOME/headsetsink, so a restart or reboot
// doesn't lose them.
type memory struct{ dir string }

func newMemory() memory {
	dir := os.Getenv("XDG_STATE_HOME")
	if dir == "" {
		dir = filepath.Join(os.Getenv("HOME"), ".local/state")
	}
	return memory{filepath.Join(dir, "headsetsink")}
}

func (m memory) path(kind string) string { return filepath.Join(m.dir, "previous-"+kind) }

func (m memory) load(kind string) string {
	b, _ := os.ReadFile(m.path(kind))
	return strings.TrimSpace(string(b))
}

func (m memory) save(kind, name string) {
	if name == "" || name == m.load(kind) {
		return
	}
	tmp := m.path(kind) + ".tmp"
	err := os.MkdirAll(m.dir, 0o755)
	if err == nil {
		err = os.WriteFile(tmp, []byte(name+"\n"), 0o644)
	}
	if err == nil {
		err = os.Rename(tmp, m.path(kind))
	}
	if err != nil {
		log.Printf("remember %s: %v", kind, err)
	}
}

// remember saves the current default of kind unless it belongs to the
// headset, so turning the headset off has somewhere to go. Until the headset
// has been found there's no telling, so nothing is saved.
func (m memory) remember(kind string, headset func() string) {
	id := headset()
	if id == "" {
		return
	}
	cur := getDefault(kind)
	nodes, err := listNodes(kind)
	if err != nil || cur == "" {
		return
	}
	if n, ok := lookup(nodes, cur); ok && !n.isMonitor() && n.usbID() != id {
		m.save(kind, cur)
	}
}

// track follows `pactl subscribe` and remembers every change of default to
// something other than the headset, whether made from a picker, pavucontrol
// or WirePlumber. Server events are what a default change produces; they are
// rare, so re-reading on each is cheap.
func (m memory) track(kinds []string, headset func() string) {
	for {
		cmd := exec.Command("pactl", "subscribe")
		out, err := cmd.StdoutPipe()
		if err == nil {
			err = cmd.Start()
		}
		if err != nil {
			log.Printf("pactl subscribe: %v", err)
			time.Sleep(5 * time.Second)
			continue
		}
		for _, k := range kinds {
			m.remember(k, headset)
		}
		sc := bufio.NewScanner(out)
		for sc.Scan() {
			if strings.Contains(sc.Text(), "on server") {
				for _, k := range kinds {
					m.remember(k, headset)
				}
			}
		}
		cmd.Wait()
		time.Sleep(2 * time.Second) // PipeWire restarting; try again
	}
}
