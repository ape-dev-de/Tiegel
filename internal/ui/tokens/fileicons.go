package tokens

import "image/color"

// FileIconPalette holds the colors of the multi-color file, folder and
// file-type icons (see package internal/ui/fileicons). Badge fills carry
// the language color; InkLight and InkDark are the monogram colors drawn
// on them.
type FileIconPalette struct {
	// Monogram and pictogram ink on badges.
	InkLight color.NRGBA // on saturated or dark fills
	InkDark  color.NRGBA // on bright fills (yellow, amber)

	// Language and file-type badge fills.
	PHP        color.NRGBA
	TypeScript color.NRGBA
	JavaScript color.NRGBA
	// JavaScriptLine draws JavaScript outlines (config files), darker
	// than JavaScript on light backgrounds.
	JavaScriptLine color.NRGBA
	JSON           color.NRGBA
	YAML           color.NRGBA
	Markdown       color.NRGBA
	HTML           color.NRGBA
	CSS            color.NRGBA
	SCSS           color.NRGBA
	XML            color.NRGBA
	SQL            color.NRGBA
	Shell          color.NRGBA
	ShellInk       color.NRGBA // prompt color on the shell badge
	Docker         color.NRGBA
	Compose        color.NRGBA
	Env            color.NRGBA
	Git            color.NRGBA
	Config         color.NRGBA // neutral config files (.editorconfig, .ini …)
	Composer       color.NRGBA
	Package        color.NRGBA // package.json and friends
	Make           color.NRGBA
	Go             color.NRGBA
	Python         color.NRGBA
	PythonInk      color.NRGBA
	Twig           color.NRGBA
	Image          color.NRGBA
	ImageSun       color.NRGBA
	Archive        color.NRGBA
	Data           color.NRGBA // csv, toml, ini
	Generic        color.NRGBA // unknown files and plain text outlines

	// Test marks the test variant of a badge (band on top).
	Test color.NRGBA

	// Folder fills by role.
	Folder         color.NRGBA
	FolderSource   color.NRGBA
	FolderTest     color.NRGBA
	FolderResource color.NRGBA
	FolderExcluded color.NRGBA
	FolderLibrary  color.NRGBA
}

// FileIcons returns the file-icon palette for mode.
func FileIcons(m Mode) FileIconPalette {
	if m == ModeLight {
		return lightFileIcons()
	}
	return darkFileIcons()
}

func darkFileIcons() FileIconPalette {
	return FileIconPalette{
		InkLight:       Hex(0xFFFFFF),
		InkDark:        Hex(0x1F2228),
		PHP:            Hex(0x8C72CC),
		TypeScript:     Hex(0x3A74C4),
		JavaScript:     Hex(0xD9B840),
		JavaScriptLine: Hex(0xD9B840),
		JSON:           Hex(0xD1983A),
		YAML:           Hex(0xC4584C),
		Markdown:       Hex(0x5B87A6),
		HTML:           Hex(0xD9703C),
		CSS:            Hex(0x3F98CF),
		SCSS:           Hex(0xC95A94),
		XML:            Hex(0x3A9C86),
		SQL:            Hex(0xB98A4E),
		Shell:          Hex(0x4A515C),
		ShellInk:       Hex(0x8FD48C),
		Docker:         Hex(0x2F8CD2),
		Compose:        Hex(0x2B7FA0),
		Env:            Hex(0x9A963A),
		Git:            Hex(0xDF6348),
		Config:         Hex(0x7D8591),
		Composer:       Hex(0x8F6B4A),
		Package:        Hex(0xC64A4A),
		Make:           Hex(0x8A7458),
		Go:             Hex(0x2FA6C4),
		Python:         Hex(0x3C6EA8),
		PythonInk:      Hex(0xF3D35E),
		Twig:           Hex(0x6E9B3C),
		Image:          Hex(0x3E9E7E),
		ImageSun:       Hex(0xF0CC5A),
		Archive:        Hex(0xA4845A),
		Data:           Hex(0x6F8C5C),
		Generic:        Hex(0x9AA1AC),
		Test:           Hex(0x5DBB63),

		Folder:         Hex(0x8C95A3),
		FolderSource:   Hex(0x5B95DA),
		FolderTest:     Hex(0x5DBB63),
		FolderResource: Hex(0xB48AD8),
		FolderExcluded: Hex(0xD48662),
		FolderLibrary:  Hex(0xA08C6A),
	}
}

func lightFileIcons() FileIconPalette {
	return FileIconPalette{
		InkLight:       Hex(0xFFFFFF),
		InkDark:        Hex(0x1F2228),
		PHP:            Hex(0x7556BC),
		TypeScript:     Hex(0x2D65B4),
		JavaScript:     Hex(0xE2BC2E),
		JavaScriptLine: Hex(0xA4850E),
		JSON:           Hex(0xD99A2B),
		YAML:           Hex(0xB8443A),
		Markdown:       Hex(0x4A7696),
		HTML:           Hex(0xD0612A),
		CSS:            Hex(0x2B86C0),
		SCSS:           Hex(0xB8457F),
		XML:            Hex(0x2A8A74),
		SQL:            Hex(0xA4743A),
		Shell:          Hex(0x3C424B),
		ShellInk:       Hex(0x8FD48C),
		Docker:         Hex(0x2079C0),
		Compose:        Hex(0x1F6E8E),
		Env:            Hex(0x87822A),
		Git:            Hex(0xD04F34),
		Config:         Hex(0x6B7380),
		Composer:       Hex(0x7E5A3A),
		Package:        Hex(0xB83A3A),
		Make:           Hex(0x7A6446),
		Go:             Hex(0x1E94B2),
		Python:         Hex(0x2F5F98),
		PythonInk:      Hex(0xF3D35E),
		Twig:           Hex(0x5B8A2C),
		Image:          Hex(0x2E8A6C),
		ImageSun:       Hex(0xF0C84A),
		Archive:        Hex(0x92724A),
		Data:           Hex(0x5C7A4A),
		Generic:        Hex(0x7A828E),
		Test:           Hex(0x3FA046),

		Folder:         Hex(0x8A93A1),
		FolderSource:   Hex(0x3E7FCC),
		FolderTest:     Hex(0x3FA046),
		FolderResource: Hex(0x9A6CC4),
		FolderExcluded: Hex(0xCC6E44),
		FolderLibrary:  Hex(0x94805C),
	}
}
