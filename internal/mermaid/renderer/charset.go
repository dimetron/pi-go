package renderer

// CharSet holds box-drawing and diagram characters for rendering.
type CharSet struct {
	TopLeft     rune
	TopRight    rune
	BottomLeft  rune
	BottomRight rune
	Horizontal  rune
	Vertical    rune

	RoundTopLeft     rune
	RoundTopRight    rune
	RoundBottomLeft  rune
	RoundBottomRight rune

	ArrowRight rune
	ArrowLeft  rune
	ArrowDown  rune
	ArrowUp    rune

	LineHorizontal rune
	LineVertical   rune
	LineDottedH    rune
	LineDottedV    rune
	LineThickH     rune
	LineThickV     rune

	CornerTopLeft     rune
	CornerTopRight    rune
	CornerBottomLeft  rune
	CornerBottomRight rune

	TeeRight rune
	TeeLeft  rune
	TeeDown  rune
	TeeUp    rune
	Cross    rune

	DiamondTop    rune
	DiamondBottom rune
	DiamondLeft   rune
	DiamondRight  rune

	CircleEndpoint rune
	CrossEndpoint  rune

	SGTopLeft     rune
	SGTopRight    rune
	SGBottomLeft  rune
	SGBottomRight rune
	SGHorizontal  rune
	SGVertical    rune

	// Shape indicators mark a node's mermaid shape in its top-left corner, so
	// a rounded box reads as distinct from a stadium in plain text. They were
	// hardcoded to their Unicode glyphs, which leaked non-ASCII runes into
	// --ascii output; routing them through the charset is what makes that mode
	// honest.
	IndRounded      rune
	IndStadium      rune
	IndSubroutine   rune
	IndDiamond      rune
	IndHexagon      rune
	IndCircle       rune
	IndDoubleCircle rune
}

// UNICODE is the default character set using Unicode box-drawing characters.
var UNICODE = CharSet{
	TopLeft: '┌', TopRight: '┐', BottomLeft: '└', BottomRight: '┘',
	Horizontal: '─', Vertical: '│',
	RoundTopLeft: '╭', RoundTopRight: '╮',
	RoundBottomLeft: '╰', RoundBottomRight: '╯',
	ArrowRight: '►', ArrowLeft: '◄', ArrowDown: '▼', ArrowUp: '▲',
	LineHorizontal: '─', LineVertical: '│',
	LineDottedH: '┄', LineDottedV: '┆',
	LineThickH: '━', LineThickV: '┃',
	CornerTopLeft: '┌', CornerTopRight: '┐',
	CornerBottomLeft: '└', CornerBottomRight: '┘',
	TeeRight: '├', TeeLeft: '┤', TeeDown: '┬', TeeUp: '┴',
	Cross:      '┼',
	DiamondTop: '◇', DiamondBottom: '◇', DiamondLeft: '◇', DiamondRight: '◇',
	CircleEndpoint: '○', CrossEndpoint: '×',
	SGTopLeft: '┌', SGTopRight: '┐',
	SGBottomLeft: '└', SGBottomRight: '┘',
	SGHorizontal: '─', SGVertical: '│',
	IndRounded: '◦', IndStadium: '⊂', IndSubroutine: '‖',
	IndDiamond: '◇', IndHexagon: '⬡', IndCircle: '○', IndDoubleCircle: '◎',
}

// ASCII is a fallback character set using only ASCII characters.
var ASCII = CharSet{
	TopLeft: '+', TopRight: '+', BottomLeft: '+', BottomRight: '+',
	Horizontal: '-', Vertical: '|',
	RoundTopLeft: '+', RoundTopRight: '+',
	RoundBottomLeft: '+', RoundBottomRight: '+',
	ArrowRight: '>', ArrowLeft: '<', ArrowDown: 'v', ArrowUp: '^',
	LineHorizontal: '-', LineVertical: '|',
	LineDottedH: '.', LineDottedV: ':',
	LineThickH: '=', LineThickV: 'H',
	CornerTopLeft: '+', CornerTopRight: '+',
	CornerBottomLeft: '+', CornerBottomRight: '+',
	TeeRight: '+', TeeLeft: '+', TeeDown: '+', TeeUp: '+',
	Cross:      '+',
	DiamondTop: '/', DiamondBottom: '\\', DiamondLeft: '/', DiamondRight: '\\',
	CircleEndpoint: 'o', CrossEndpoint: 'x',
	SGTopLeft: '+', SGTopRight: '+',
	SGBottomLeft: '+', SGBottomRight: '+',
	SGHorizontal: '-', SGVertical: '|',
	IndRounded: 'o', IndStadium: '(', IndSubroutine: '=',
	IndDiamond: '<', IndHexagon: '#', IndCircle: 'O', IndDoubleCircle: '@',
}
