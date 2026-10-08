package main

// Palette is the set of colors a consumer draws the demo with. The sixteen
// ANSI entries are Windows Terminal's Campbell scheme, which is the scheme the
// renderer's muted red is derived from. The last four are the mock terminal's
// own chrome, not status-line colors. The JSON keys double as the color names
// a Run carries.
type Palette struct {
	Background    string `json:"background"`
	Foreground    string `json:"foreground"`
	Dim           string `json:"dim"`
	Black         string `json:"black"`
	Red           string `json:"red"`
	Green         string `json:"green"`
	Yellow        string `json:"yellow"`
	Blue          string `json:"blue"`
	Magenta       string `json:"magenta"`
	Cyan          string `json:"cyan"`
	White         string `json:"white"`
	BrightBlack   string `json:"brightBlack"`
	BrightRed     string `json:"brightRed"`
	BrightGreen   string `json:"brightGreen"`
	BrightYellow  string `json:"brightYellow"`
	BrightBlue    string `json:"brightBlue"`
	BrightMagenta string `json:"brightMagenta"`
	BrightCyan    string `json:"brightCyan"`
	BrightWhite   string `json:"brightWhite"`
	Reply         string `json:"reply"`
	Spinner       string `json:"spinner"`
	PromptRule    string `json:"promptRule"`
	ModeLine      string `json:"modeLine"`
}

// palette is the palette written into scenes.json.
var palette = Palette{
	Background:    "#0c0c0c",
	Foreground:    "#cccccc",
	Dim:           "#767676",
	Black:         "#0c0c0c",
	Red:           "#c50f1f",
	Green:         "#13a10e",
	Yellow:        "#c19c00",
	Blue:          "#0037da",
	Magenta:       "#881798",
	Cyan:          "#3a96dd",
	White:         "#cccccc",
	BrightBlack:   "#767676",
	BrightRed:     "#e74856",
	BrightGreen:   "#16c60c",
	BrightYellow:  "#f9f1a5",
	BrightBlue:    "#3b78ff",
	BrightMagenta: "#b4009e",
	BrightCyan:    "#61d6d6",
	BrightWhite:   "#f2f2f2",
	Reply:         "#f2f2f2",
	Spinner:       "#d77757",
	PromptRule:    "#4eba65",
	ModeLine:      "#ff6b80",
}
