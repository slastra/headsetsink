# headsetsink

Turn a Logitech wireless headset on and Linux audio moves to it. Turn it off
and audio goes back to the speakers you were using.

## Why

A wireless headset like the G733 talks to a USB dongle that stays plugged in.
PipeWire sees the dongle, not the headset, so the headset's sink exists
whether the headset is on or off. Nothing switches when you put it on, and
when you turn it off your audio keeps playing into a headset that isn't
there.

headsetsink listens to the dongle itself. When the headset links up or drops,
the dongle reports it within a second or two, and headsetsink changes the
default sink to match.

## Install

Needs Go 1.22+, PipeWire with `pipewire-pulse`, and `pactl` 16 or newer (for
`pactl -f json`).

```sh
go install github.com/slastra/headsetsink@latest
headsetsink -check
```

`-check` finds the headset and prints what it sees:

```
headset            Logitech G733 Gaming Headset (046d:0afe)
hidraw             /dev/hidraw10
power              on, 3775 mV
sink               alsa_output.usb-Logitech_G733_Gaming_Headset_0000000000000000-00.analog-stereo
default sink       alsa_output.usb-Logitech_G733_Gaming_Headset_0000000000000000-00.analog-stereo
remembered sink    alsa_output.pci-0000_00_1f.3.analog-stereo
source             alsa_input.usb-Logitech_G733_Gaming_Headset_0000000000000000-00.mono-fallback
default source     alsa_input.pci-0000_00_1f.3.analog-stereo
remembered source  -
```

### Access

headsetsink opens the dongle's hidraw node as you. If `-check` reports
`permission denied`, install a udev rule. If you have
[HeadsetControl](https://github.com/Sapd/HeadsetControl) installed, its rules
already cover this. Otherwise:

```sh
sudo install -m 644 udev/70-headsetsink.rules /etc/udev/rules.d/
sudo udevadm control --reload
sudo udevadm trigger --subsystem-match=hidraw --attr-match=idVendor=046d
```

The rule names the G733's dongle (`046d:0afe`). For another model, change the
product id to your dongle's, from `lsusb`.

### Run at login

```sh
mkdir -p ~/.config/systemd/user
cp systemd/headsetsink.service ~/.config/systemd/user/
systemctl --user enable --now headsetsink
journalctl --user -u headsetsink -f
```

## Usage

| Flag | |
|---|---|
| `-mic` | Also switch the default source (microphone). Off by default, since many people keep a separate mic. |
| `-fallback SINK` | Sink to use on power-off if the remembered one has gone away (a TV that's off, say). Takes a sink name from `pactl list short sinks`. |
| `-hook CMD` | Run `CMD on` or `CMD off` after each switch, for a sound or a notification. |
| `-usb VID:PID` | Watch this dongle instead of searching for one. |
| `-check` | Print what was found and the headset's state, then exit. Exits 1 if no headset is found. |
| `-v` | Log every voltage event from the headset. |

### What it remembers

The sink to go back to is the last default that wasn't the headset. headsetsink
watches `pactl subscribe`, so any change of default counts, whether you made it
in pavucontrol, a desktop picker, or anything else. It's kept in
`$XDG_STATE_HOME/headsetsink/` (usually `~/.local/state/headsetsink/`), so it
survives restarts and reboots.

If you move audio somewhere else yourself while the headset is on, turning the
headset off leaves that alone. It only switches back when the headset is still
the default.

## Supported headsets

Tested on the **Logitech G733** (`046d:0afe`).

Without `-usb`, headsetsink considers every Logitech USB device that has a
PipeWire sink and uses the first whose hidraw node answers HID++ with feature
`0x1F20`. Other Logitech wireless headsets that report battery voltage this
way should work too, and `-check` will tell you. If yours does, or doesn't,
please open an issue with the `-check` output and the model.

## How it works

- **The signal.** The dongle speaks HID++ 2.0 on a hidraw node. Its feature
  `0x1F20` (battery voltage, ADC measurement) sends an unsolicited event when
  the headset links up, carrying the voltage in millivolts, and another with a
  voltage of 0 when it drops. headsetsink looks the feature's index up through
  the root feature instead of assuming one.
- **A false "off".** The G733 can also send a zero-voltage event a few
  seconds after power-on while it is still linked. So a zero only starts a
  2 second timer; after it, headsetsink asks for the voltage directly. A
  headset that is really off makes the dongle answer with an error or 0, and one
  that is still on answers with its voltage.
- **Coming back in range.** While the headset is off, headsetsink also asks
  for the voltage every 2 seconds, in case the headset reconnects without
  sending an event right away. The log says which one noticed: `event`,
  `poll`, `confirmed` (a checked zero) or `startup`.
- **Audio.** Everything on the PipeWire side goes through `pactl`. The
  headset's sink and source are found by the USB vendor and product ids in
  their properties (monitor sources share those ids and are skipped), so
  nothing depends on the long ALSA node names.
- **Dongle unplugged.** headsetsink notices the hidraw node closing, looks
  for a headset every 3 seconds, and picks it back up when it returns.

## Files

| File | |
|---|---|
| `main.go` | Flags, the watch loop, switching, `-check` |
| `hidpp.go` | HID++ requests and events, finding hidraw nodes |
| `audio.go` | `pactl` sinks and sources, defaults, what to go back to |
| `systemd/` | User unit |
| `udev/` | Access rule for the G733 dongle |

## License

MIT
