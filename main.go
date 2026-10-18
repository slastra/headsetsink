// headsetsink makes a Logitech wireless headset the default audio sink when it
// powers on, and puts the previous sink back when it powers off.
//
// The USB dongle stays enumerated whether or not the headset is on, so its
// PipeWire sink never goes away and can't be the signal. Instead we listen on
// the dongle's HID++ interface: feature 0x1F20 (battery voltage) sends an
// event when the headset links up (voltage > 0) and when it drops (0).
package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"regexp"
	"sync/atomic"
	"time"
)

const (
	sysfs = "/sys"
	// A zero-voltage event also turns up a few seconds after power-on while
	// the headset is linked, so a zero is only believed once a direct query
	// this much later agrees.
	offConfirm = 2 * time.Second
	// The headset doesn't always announce itself when it comes back in
	// range: the voltage event can trail the reconnect by 15-30 s. While
	// it's off we ask every this often instead of waiting.
	offPoll = 2 * time.Second
)

var (
	usbFlag  = flag.String("usb", "", "vendor:product of the dongle (default: find a Logitech headset)")
	mic      = flag.Bool("mic", false, "also switch the default source (microphone)")
	fallback = flag.String("fallback", "", "sink to use on power-off when the remembered one is gone")
	hook     = flag.String("hook", "", "command run with \"on\" or \"off\" after each switch")
	check    = flag.Bool("check", false, "print what was found and the headset's state, then exit")
	verbose  = flag.Bool("v", false, "log every voltage event")
)

// current is the USB id of the headset being watched, "" while there is none.
var current atomic.Value

func headsetID() string {
	id, _ := current.Load().(string)
	return id
}

func main() {
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "usage: headsetsink [flags]\n\n"+
			"Switch the default audio sink to a Logitech wireless headset when it\n"+
			"powers on, and back to the previous sink when it powers off.\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()
	log.SetFlags(0)
	if *usbFlag != "" && !regexp.MustCompile(`^[0-9a-fA-F]{4}:[0-9a-fA-F]{4}$`).MatchString(*usbFlag) {
		log.Fatalf("-usb %q: want vendor:product, like 046d:0afe", *usbFlag)
	}
	if *check {
		os.Exit(runCheck())
	}

	mem := newMemory()
	kinds := []string{"sink"}
	if *mic {
		kinds = append(kinds, "source")
	}
	current.Store("")
	go mem.track(kinds, headsetID)

	waiting := false
	for {
		id, path, err := find()
		if err != nil {
			if !waiting {
				log.Printf("waiting for a headset: %v", err)
				waiting = true
			}
			time.Sleep(3 * time.Second)
			continue
		}
		waiting = false
		log.Printf("watching %s (%s) on %s", hidName(sysfs, path), id, path)
		current.Store(id)
		if err := watch(id, path, mem, kinds); err != nil {
			log.Printf("%s: %v", path, err)
		}
		current.Store("")
		time.Sleep(time.Second)
	}
}

// find returns the headset's USB id and the hidraw node that answers HID++
// with the voltage feature. Without -usb, any Logitech device with a sink is
// a candidate.
func find() (id, path string, err error) {
	ids := []string{*usbFlag}
	if *usbFlag == "" {
		sinks, err := listNodes("sink")
		if err != nil {
			return "", "", err
		}
		if ids = candidates(sinks); len(ids) == 0 {
			return "", "", errors.New("no Logitech USB audio device")
		}
	}
	for _, id := range ids {
		for _, p := range hidrawNodes(sysfs, id) {
			d, err := openHIDPP(p)
			if err != nil {
				return "", "", err // most likely permissions; say so
			}
			_, err = d.featureIndex(featureVoltage)
			d.Close()
			if err == nil {
				return id, p, nil
			}
		}
	}
	return "", "", errors.New("no hidraw node answers HID++ with feature 0x1F20")
}

func watch(id, path string, mem memory, kinds []string) error {
	d, err := openHIDPP(path)
	if err != nil {
		return err
	}
	defer d.Close()
	idx, err := d.featureIndex(featureVoltage)
	if err != nil {
		return err
	}

	var on, known bool
	set := func(now bool, why string) {
		if known && now == on {
			return
		}
		on, known = now, true
		apply(id, on, why, mem, kinds)
	}
	poll := time.NewTicker(offPoll)
	defer poll.Stop()
	recheck := time.NewTimer(0)
	<-recheck.C
	event := func(b []byte) {
		mv, ok := voltageEvent(b, idx)
		if !ok {
			return
		}
		if *verbose {
			log.Printf("voltage event %d mV, flags %02x", mv, b[6])
		}
		if mv > 0 {
			recheck.Stop()
			set(true, "event")
		} else {
			recheck.Reset(offConfirm)
		}
	}

	set(d.voltage(idx, event) > 0, "startup")
	for {
		select {
		case b, ok := <-d.reports:
			if !ok {
				return errors.New("device closed")
			}
			event(b)
		case <-recheck.C:
			now := d.voltage(idx, event) > 0
			if now && on {
				log.Printf("zero-voltage event, but the headset still answers; ignoring")
			}
			set(now, "confirmed")
		case <-poll.C:
			if !on && d.voltage(idx, event) > 0 {
				set(true, "poll")
			}
		}
	}
}

func apply(id string, on bool, why string, mem memory, kinds []string) {
	state := map[bool]string{true: "on", false: "off"}[on]
	log.Printf("headset %s (%s)", state, why)
	for _, kind := range kinds {
		switchDefault(kind, id, on, mem)
	}
	if *hook != "" {
		if out, err := exec.Command(*hook, state).CombinedOutput(); err != nil {
			log.Printf("hook: %v %s", err, out)
		}
	}
}

// switchDefault moves the default sink or source to the headset when it turns
// on, and back to the remembered one when it turns off. If something else was
// chosen while the headset was on, power-off leaves that choice alone.
func switchDefault(kind, id string, on bool, mem memory) {
	nodes, err := listNodes(kind)
	if err != nil {
		log.Print(err)
		return
	}
	cur := getDefault(kind)
	if on {
		target := headsetNode(nodes, id)
		if target == "" {
			log.Printf("no %s for %s", kind, id)
			return
		}
		if cur != target {
			if n, ok := lookup(nodes, cur); ok && !n.isMonitor() {
				mem.save(kind, cur)
			}
			setDefault(kind, target)
		}
		return
	}

	if n, ok := lookup(nodes, cur); !ok || n.usbID() != id {
		return
	}
	prev := mem.load(kind)
	if _, ok := lookup(nodes, prev); !ok {
		if kind == "sink" && *fallback != "" {
			if _, ok := lookup(nodes, *fallback); ok {
				setDefault(kind, *fallback)
				return
			}
		}
		log.Printf("no previous %s to go back to (remembered %q)", kind, prev)
		return
	}
	setDefault(kind, prev)
}

func runCheck() int {
	id, path, err := find()
	if err != nil {
		fmt.Println("no headset:", err)
		return 1
	}
	d, err := openHIDPP(path)
	if err != nil {
		fmt.Println(err)
		return 1
	}
	defer d.Close()
	idx, err := d.featureIndex(featureVoltage)
	if err != nil {
		fmt.Println(err)
		return 1
	}
	mv := d.voltage(idx, nil)
	power := "off"
	if mv > 0 {
		power = fmt.Sprintf("on, %d mV", mv)
	}
	mem := newMemory()
	row := func(k, v string) {
		if v == "" {
			v = "-"
		}
		fmt.Printf("%-18s %s\n", k, v)
	}
	row("headset", fmt.Sprintf("%s (%s)", hidName(sysfs, path), id))
	row("hidraw", path)
	row("power", power)
	for _, kind := range []string{"sink", "source"} {
		nodes, _ := listNodes(kind)
		row(kind, headsetNode(nodes, id))
		row("default "+kind, getDefault(kind))
		row("remembered "+kind, mem.load(kind))
	}
	return 0
}
