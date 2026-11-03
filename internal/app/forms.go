package app

import (
	"fmt"
	"strconv"

	"github.com/egoist/mygo/ui"

	"github.com/CapyTown/AirDroid/internal/config"
	"github.com/CapyTown/AirDroid/locales"
)

// Grouped settings, as macOS's System Settings and Pulse lay them out: rows
// in rounded sections, a title on the left with a line of explanation
// under it, the control on the right, and notes below a section.

func sectionFill(t *ui.Theme) ui.Color {
	if t.Dark {
		return ui.RGBA(255, 255, 255, 0.06)
	}
	return ui.RGBA(0, 0, 0, 0.045)
}

func section(c *ui.Context, fn func()) ui.Element {
	t := c.Theme()
	return ui.Column(c).FillWidth().Radius(12).Background(sectionFill(t)).
		PaddingX(16).Dividers(1, t.Border.Alpha(0.6)).Children(fn)
}

func sectionNote(c *ui.Context, text string) {
	ui.Text(c, text).FontSize(12).TextColor(c.Theme().TextMuted).PaddingX(16).MarginY(-6)
}

// formRow lays out a row of a section.
func formRow(c *ui.Context, title, subtitle string, control func()) ui.Element {
	t := c.Theme()
	return ui.Row(c).FillWidth().MinHeight(48).PaddingY(10).Gap(16).AlignItems(ui.Center).Children(func() {
		ui.Column(c).Grow(1).Shrink(1).Gap(2).Children(func() {
			ui.Text(c, title).FontSize(13)
			if subtitle != "" {
				ui.Text(c, subtitle).FontSize(11.5).TextColor(t.TextMuted).Wrap()
			}
		})
		if control != nil {
			control()
		}
	})
}

// switchRow is a row whose control is a switch, and reports a change.
func switchRow(c *ui.Context, key, title, subtitle string, on *bool) bool {
	changed := false
	formRow(c, title, subtitle, func() {
		changed = ui.Switch(c.Key(key), on).Label(title).Changed()
	})
	return changed
}

// segmentedRow chooses one of a few values.
func segmentedRow[T comparable](c *ui.Context, key, title, subtitle string, value *T, values []T, labels []string) bool {
	idx := 0
	for i, v := range values {
		if v == *value {
			idx = i
		}
	}
	before := idx
	formRow(c, title, subtitle, func() {
		ui.Segmented(c.Key(key), &idx, labels...).Label(title)
	})
	if idx != before {
		*value = values[idx]
		return true
	}
	return false
}

// selectRow chooses one of a list.
func selectRow[T comparable](c *ui.Context, key, title, subtitle string, value *T, values []T, labels []string) bool {
	chosen := ""
	for i, v := range values {
		if v == *value {
			chosen = labels[i]
		}
	}
	if chosen == "" && len(labels) > 0 {
		chosen = labels[0]
	}
	before := chosen
	formRow(c, title, subtitle, func() {
		ui.Select(c.Key(key), &chosen, labels).Label(title).MinWidth(150)
	})
	if chosen != before {
		for i, l := range labels {
			if l == chosen {
				*value = values[i]
				return true
			}
		}
	}
	return false
}

var (
	maxSizes   = []int{0, 2560, 1920, 1600, 1280, 1024, 800}
	bitRates   = []int{2, 4, 8, 12, 16, 24, 32, 48}
	frameRates = []int{30, 60, 90, 120}
	codecs     = []string{config.CodecH264, config.CodecH265}
)

// sizeLabels names maxSizes: the device's own resolution, then pixels.
func sizeLabels() []string {
	out := make([]string, len(maxSizes))
	for i, s := range maxSizes {
		if s == 0 {
			out[i] = locales.DeviceResolution()
		} else {
			out[i] = fmt.Sprintf("%d px", s)
		}
	}
	return out
}

func bitRateLabels() []string {
	out := make([]string, len(bitRates))
	for i, b := range bitRates {
		out[i] = fmt.Sprintf("%d Mbps", b)
	}
	return out
}

func frameRateLabels() []string {
	out := make([]string, len(frameRates))
	for i, f := range frameRates {
		out[i] = strconv.Itoa(f)
	}
	return out
}

// mirrorOptionsForm edits mirroring options, and reports a change. compact
// lays it out for the sidebar's popover.
func mirrorOptionsForm(c *ui.Context, o *config.MirrorOptions, compact bool) bool {
	changed := false
	sub := func(s string) string {
		if compact {
			return ""
		}
		return s
	}
	sectionHeading(c, locales.SectionVideo())
	section(c, func() {
		changed = selectRow(c, "maxSize", locales.Resolution(), sub(locales.ResolutionNote()), &o.MaxSize, maxSizes, sizeLabels()) || changed
		changed = selectRow(c, "bitRate", locales.BitRate(), sub(locales.BitRateNote()), &o.BitRateMbps, bitRates, bitRateLabels()) || changed
		changed = segmentedRow(c, "fps", locales.FrameRate(), sub(locales.FrameRateNote()), &o.MaxFPS, frameRates, frameRateLabels()) || changed
		changed = segmentedRow(c, "codec", locales.Codec(), sub(locales.CodecNote()), &o.Codec, codecs, []string{"H.264", "H.265"}) || changed
	})
	sectionHeading(c, locales.SectionPhone())
	section(c, func() {
		changed = switchRow(c, "audio", locales.DeviceAudio(), sub(locales.DeviceAudioNote(thisComputer())), &o.Audio) || changed
		changed = switchRow(c, "screenOff", locales.TurnScreenOff(), sub(locales.TurnScreenOffNote()), &o.TurnScreenOff) || changed
		changed = switchRow(c, "stayAwake", locales.StayAwake(), sub(locales.StayAwakeNote()), &o.StayAwake) || changed
		changed = switchRow(c, "touches", locales.ShowTouches(), sub(locales.ShowTouchesNote()), &o.ShowTouches) || changed
		changed = switchRow(c, "onTop", locales.KeepOnTop(), sub(locales.KeepOnTopNote()), &o.AlwaysOnTop) || changed
		if !compact {
			changed = switchRow(c, "powerOff", locales.PowerOffOnClose(), locales.PowerOffOnCloseNote(), &o.PowerOffOnClose) || changed
		}
	})
	return changed
}
