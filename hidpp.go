package main

// Just enough HID++ 2.0 to look a feature up and call it, over the dongle's
// hidraw node.

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	reportLong = 0x11 // 20-byte HID++ report
	deviceIdx  = 0xff // the device itself, not a receiver slot
	swID       = 0x0b // tags our requests; unsolicited events carry swid 0
	reqTimeout = time.Second

	featureVoltage = 0x1F20 // battery voltage (ADC measurement)
)

type hidpp struct {
	f       *os.File
	reports chan []byte
}

func openHIDPP(path string) (*hidpp, error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	d := &hidpp{f: f, reports: make(chan []byte, 32)}
	go func() {
		buf := make([]byte, 64)
		for {
			n, err := f.Read(buf)
			if err != nil {
				close(d.reports)
				return
			}
			d.reports <- append([]byte(nil), buf[:n]...)
		}
	}()
	return d, nil
}

func (d *hidpp) Close() { d.f.Close() }

// call sends function fn of the feature at idx and waits for its response.
// Reports that aren't the response go to onOther, so events aren't lost.
func (d *hidpp) call(idx, fn byte, params []byte, onOther func([]byte)) ([]byte, error) {
	msg := make([]byte, 20)
	msg[0], msg[1], msg[2], msg[3] = reportLong, deviceIdx, idx, fn<<4|swID
	copy(msg[4:], params)
	if _, err := d.f.Write(msg); err != nil {
		return nil, err
	}
	deadline := time.After(reqTimeout)
	for {
		select {
		case b, ok := <-d.reports:
			if !ok {
				return nil, errors.New("device closed")
			}
			if len(b) >= 8 && b[0] == reportLong && b[2] == idx && b[3] == msg[3] {
				return b, nil
			}
			// HID++ 2.0 errors come back as feature 0xff, 1.0 errors as 0x8f.
			if len(b) >= 6 && b[0] == reportLong && (b[2] == 0xff || b[2] == 0x8f) && b[3] == idx && b[4] == msg[3] {
				return nil, fmt.Errorf("HID++ error 0x%02x", b[5])
			}
			if onOther != nil {
				onOther(b)
			}
		case <-deadline:
			return nil, errors.New("timeout")
		}
	}
}

// featureIndex asks the root feature where feature lives on this device.
func (d *hidpp) featureIndex(feature uint16) (byte, error) {
	b, err := d.call(0x00, 0, binary.BigEndian.AppendUint16(nil, feature), nil)
	if err != nil {
		return 0, err
	}
	if b[4] == 0 {
		return 0, fmt.Errorf("feature 0x%04x not supported", feature)
	}
	return b[4], nil
}

// voltage reads the battery voltage in millivolts. With the headset off the
// dongle answers with an error or 0; both come back as 0.
func (d *hidpp) voltage(idx byte, onOther func([]byte)) uint16 {
	b, err := d.call(idx, 0, nil, onOther)
	if err != nil {
		return 0
	}
	return binary.BigEndian.Uint16(b[4:6])
}

// voltageEvent reports whether b is an unsolicited voltage event from the
// feature at idx, and the millivolts it carries.
func voltageEvent(b []byte, idx byte) (mv uint16, ok bool) {
	if len(b) < 7 || b[0] != reportLong || b[2] != idx || b[3] != 0x00 {
		return 0, false
	}
	return binary.BigEndian.Uint16(b[4:6]), true
}

// hidrawNodes lists the hidraw nodes of USB device id ("046d:0afe") under the
// sysfs root, as /dev paths.
func hidrawNodes(sysfs, id string) []string {
	vid, pid, _ := strings.Cut(strings.ToUpper(id), ":")
	want := []byte(fmt.Sprintf("HID_ID=0003:0000%s:0000%s\n", vid, pid))
	dirs, _ := filepath.Glob(filepath.Join(sysfs, "class/hidraw/hidraw*"))
	var nodes []string
	for _, dir := range dirs {
		ev, err := os.ReadFile(filepath.Join(dir, "device/uevent"))
		if err == nil && bytes.Contains(ev, want) {
			nodes = append(nodes, "/dev/"+filepath.Base(dir))
		}
	}
	return nodes
}

// hidName returns the HID_NAME of a hidraw node, for display.
func hidName(sysfs, node string) string {
	ev, _ := os.ReadFile(filepath.Join(sysfs, "class/hidraw", filepath.Base(node), "device/uevent"))
	for _, line := range strings.Split(string(ev), "\n") {
		if name, ok := strings.CutPrefix(line, "HID_NAME="); ok {
			return name
		}
	}
	return ""
}
