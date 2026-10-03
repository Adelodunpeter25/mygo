package native

import (
	"strconv"

	"github.com/egoist/mygo/plugins/updater/internal/markdown"
	"github.com/egoist/mygo/ui"
)

// notes builds release notes, in the manner of the page of package
// updater. Their text is selectable, and links open in the browser.
func notes(c *ui.Context, t *ui.Theme, blocks []markdown.Block) {
	ui.Column(c).Children(func() { buildBlocks(c, t, blocks, false, 0) })
}

// buildBlocks builds blocks, apart by the room between paragraphs, or by
// none in the items of a list (tight). depth counts the lists around them.
func buildBlocks(c *ui.Context, t *ui.Theme, blocks []markdown.Block, tight bool, depth int) {
	for i, b := range blocks {
		var gap float32
		switch {
		case i == 0 || tight:
		case b.Kind == markdown.Heading:
			gap = 12
		case blocks[i-1].Kind == markdown.Heading:
			gap = 4
		default:
			gap = 8
		}
		buildBlock(c, t, b, depth).Margin(gap, 0, 0, 0)
	}
}

// headingSizes are the sizes of headings of levels 1, 2 and the others,
// relative to the text.
var headingSizes = [...]float32{16.0 / 13, 14.0 / 13, 1}

func buildBlock(c *ui.Context, t *ui.Theme, b markdown.Block, depth int) *ui.Element {
	switch b.Kind {
	case markdown.Heading:
		size := headingSizes[min(b.Level, len(headingSizes))-1]
		return paragraph(c, t, b.Inlines).FontSize(t.Rem(size)).Bold()
	case markdown.List:
		return ui.Column(c).Children(func() {
			for i, item := range b.Items {
				ui.Row(c).AlignItems(ui.Start).Children(func() {
					marker := bullet(depth)
					if b.Ordered {
						marker = strconv.Itoa(i+1) + "."
					}
					ui.Text(c, marker).Width(20).Shrink(0).TextAlign(ui.End).Padding(0, 6, 0, 0)
					ui.Column(c).Grow(1).Children(func() { buildBlocks(c, t, item, true, depth+1) })
				})
			}
		})
	case markdown.Quote:
		return ui.Column(c).BorderWidth(0, 0, 0, 3).BorderColor(t.Border).Padding(0, 0, 0, 10).
			TextColor(t.TextMuted).Children(func() { buildBlocks(c, t, b.Blocks, false, depth) })
	case markdown.Code:
		return ui.ScrollHorizontal(c).Background(track(t)).Radius(3).Children(func() {
			ui.Text(c, b.Text).Font("monospace").FontSize(t.Rem(12.0/13)).NoWrap().Padding(6, 8).Selectable()
		})
	case markdown.Rule:
		return ui.Divider(c)
	}
	return paragraph(c, t, b.Inlines)
}

// paragraph builds the text of a paragraph or heading, which the user may
// select.
func paragraph(c *ui.Context, t *ui.Theme, inlines []markdown.Inline) *ui.Element {
	return ui.RichText(c).Children(func() { buildInlines(c, t, inlines) }).Selectable()
}

// bullet returns the marker of the items of a list in depth lists, as
// browsers draw them.
func bullet(depth int) string {
	switch depth {
	case 0:
		return "•"
	case 1:
		return "◦"
	}
	return "▪"
}

// buildInlines builds the text of a paragraph inside it: links open in the
// browser.
func buildInlines(c *ui.Context, t *ui.Theme, inlines []markdown.Inline) {
	for _, in := range inlines {
		switch in.Kind {
		case markdown.Text:
			ui.Text(c, in.Text)
		case markdown.CodeSpan:
			ui.Text(c, in.Text).Font("monospace").FontSize(t.Rem(12.0 / 13)).TextBackground(track(t))
		case markdown.Emphasis:
			ui.RichText(c).Italic().Children(func() { buildInlines(c, t, in.Children) })
		case markdown.Strong:
			ui.RichText(c).Bold().Children(func() { buildInlines(c, t, in.Children) })
		case markdown.Link:
			ui.Link(c, "", in.URL).Children(func() { buildInlines(c, t, in.Children) })
		}
	}
}
