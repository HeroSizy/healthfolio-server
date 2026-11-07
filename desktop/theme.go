package desktop

import (
	"time"

	"github.com/egoist/mygo/ui"
)

// The look follows Kero's "paper" theme: a soft wash under a sidebar without
// a background, and the content on one white card with a hairline and a
// gentle shadow. The accent is Apple Health's pink.
type palette struct {
	dark bool

	wash        []string
	glow        ui.Color
	text        ui.Color
	textMuted   ui.Color
	textFaint   ui.Color
	rowHover    ui.Color
	rowSelected ui.Color
	rowBorder   ui.Color
	rowShadow   ui.Color
	card        ui.Color
	cardBorder  ui.Color
	cardShadow  ui.Color
	cardShadow2 ui.Color
	inset       ui.Color // tiles and code blocks inside the card
	control     ui.Color
	controlHov  ui.Color
	divider     ui.Color
	chip        ui.Color
	accent      ui.Color
	accentSoft  ui.Color
	ok          ui.Color
	okSoft      ui.Color
	danger      ui.Color
	dangerSoft  ui.Color
	bar         ui.Color
	barFail     ui.Color
}

var lightPalette = palette{
	wash:        []string{"#f6f6f7", "#f7f7f8", "#f8f8f8"},
	glow:        ui.RGBA(255, 214, 224, 0.45),
	text:        ui.Hex("#1d1d1f"),
	textMuted:   ui.Hex("#6e6e78"),
	textFaint:   ui.Hex("#a3a3ad"),
	rowHover:    ui.RGBA(0, 0, 0, 0.04),
	rowSelected: ui.Hex("#ffffff"),
	rowBorder:   ui.RGBA(0, 0, 0, 0.05),
	rowShadow:   ui.RGBA(20, 20, 50, 0.08),
	card:        ui.Hex("#ffffff"),
	cardBorder:  ui.RGBA(0, 0, 0, 0.07),
	cardShadow:  ui.RGBA(0, 0, 0, 0.07),
	cardShadow2: ui.RGBA(0, 0, 0, 0.035),
	inset:       ui.Hex("#f6f6f8"),
	control:     ui.Hex("#ffffff"),
	controlHov:  ui.Hex("#f7f7f9"),
	divider:     ui.RGBA(0, 0, 0, 0.06),
	chip:        ui.RGBA(0, 0, 0, 0.045),
	accent:      ui.Hex("#ff2d55"),
	accentSoft:  ui.RGBA(255, 45, 85, 0.10),
	ok:          ui.Hex("#1f8a3b"),
	okSoft:      ui.RGBA(52, 199, 89, 0.14),
	danger:      ui.Hex("#d93a35"),
	dangerSoft:  ui.RGBA(217, 58, 53, 0.10),
	bar:         ui.Hex("#ff2d55"),
	barFail:     ui.Hex("#d93a35"),
}

var darkPalette = palette{
	dark:        true,
	wash:        []string{"#141416", "#151517", "#161618"},
	glow:        ui.RGBA(200, 60, 100, 0.12),
	text:        ui.Hex("#ececf0"),
	textMuted:   ui.Hex("#a9a9b4"),
	textFaint:   ui.Hex("#83838f"),
	rowHover:    ui.RGBA(255, 255, 255, 0.05),
	rowSelected: ui.RGBA(255, 255, 255, 0.10),
	rowBorder:   ui.RGBA(255, 255, 255, 0.06),
	rowShadow:   ui.RGBA(0, 0, 0, 0.25),
	card:        ui.Hex("#232328"),
	cardBorder:  ui.RGBA(255, 255, 255, 0.08),
	cardShadow:  ui.RGBA(0, 0, 0, 0.35),
	cardShadow2: ui.RGBA(0, 0, 0, 0.2),
	inset:       ui.Hex("#2b2b31"),
	control:     ui.Hex("#404047"),
	controlHov:  ui.Hex("#4a4a52"),
	divider:     ui.RGBA(255, 255, 255, 0.07),
	chip:        ui.RGBA(255, 255, 255, 0.07),
	accent:      ui.Hex("#ff5c7c"),
	accentSoft:  ui.RGBA(255, 92, 124, 0.16),
	ok:          ui.Hex("#4fd17a"),
	okSoft:      ui.RGBA(79, 209, 122, 0.15),
	danger:      ui.Hex("#ff6961"),
	dangerSoft:  ui.RGBA(255, 105, 97, 0.14),
	bar:         ui.Hex("#ff5c7c"),
	barFail:     ui.Hex("#ff6961"),
}

func paletteFor(c *ui.Context) *palette {
	if c.Theme().Dark {
		return &darkPalette
	}
	return &lightPalette
}

// themed is the toolkit's theme with our accent and borderless controls, as Kero's.
func themed(c *ui.Context, pal *palette) *ui.Theme {
	t := *c.Theme()
	t.Accent = pal.accent
	t.AccentHover = pal.accent.Mix(ui.Hex("#000000"), 0.08)
	t.AccentPressed = pal.accent.Mix(ui.Hex("#000000"), 0.16)
	t.Selection = pal.accent.Alpha(0.25)
	t.Border = ui.Transparent
	t.Surface = pal.control
	t.SurfaceHover = pal.controlHov
	t.SurfacePressed = pal.controlHov.Mix(pal.text, 0.06)
	t.Text = pal.text
	t.TextMuted = pal.textMuted
	return &t
}

// backdrop paints the window's wash and a faint pink glow from the top right.
func backdrop(c *ui.Context, row ui.Element, pal *palette) {
	stops := make([]ui.GradientStop, len(pal.wash))
	for i, h := range pal.wash {
		stops[i] = ui.GradientStop{Color: ui.Hex(h), At: float32(i) / float32(len(pal.wash)-1)}
	}
	c.Root().LinearGradient(ui.LinearGradient{Angle: 180, Oklab: true, Stops: stops})
	row.LinearGradient(ui.LinearGradient{Angle: 225, Oklab: true, Stops: []ui.GradientStop{
		{Color: pal.glow, At: 0}, {Color: pal.glow.Alpha(0), At: 0.5}}})
}

// raised gives a surface Kero's card edge: a hairline and two soft shadows.
func raised(e ui.Element, pal *palette) ui.Element {
	return e.Background(pal.card).Border(1, pal.cardBorder).
		Shadow(0, 1, 2, 0, pal.cardShadow2).Shadow(0, 8, 24, -6, pal.cardShadow)
}

// toggle is Kero's switch: a track in the accent when on, a knob that springs across.
func toggle(c *ui.Context, on *bool, pal *palette) ui.Element {
	sw := ui.SwitchBase(c, on).Size(38, 22).Radius(11).Padding(2).Shrink(0)
	track := pal.text.Alpha(0.14)
	if pal.dark {
		track = ui.RGBA(255, 255, 255, 0.2)
	}
	if *on {
		track = pal.accent
	}
	sw.Background(sw.AnimateColor("track", track, ui.Animation{Duration: 150 * time.Millisecond}))
	x := sw.AnimateTo("knob", b2f(*on)*16, ui.Animation{Spring: ui.SpringOptions{Duration: 260 * time.Millisecond, Bounce: 0.15}})
	sw.Children(func() {
		ui.Box(c).Size(18, 18).Radius(9).Background(ui.Hex("#ffffff")).Translate(x, 0).
			Shadow(0, 1, 2, 0, ui.RGBA(0, 0, 0, 0.22)).Shadow(0, 0, 0, 0.5, ui.RGBA(0, 0, 0, 0.06))
	})
	return sw
}

func b2f(b bool) float32 {
	if b {
		return 1
	}
	return 0
}
