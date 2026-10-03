# Native UI

A window can show a user interface that MyGo draws itself instead of a web
page. You write it in Go with package `ui`: there is no HTML, no JavaScript
and no frontend build, and the window starts no webview, so it opens at
once and uses little memory. MyGo draws it on the GPU, with Metal on macOS,
Direct3D 11 on Windows and OpenGL on Linux.

```go
package main

import (
	"fmt"
	"log"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

type counter struct{ n int }

func (s *counter) view(c *ui.Context) {
	ui.Column(c).Fill().Center().Gap(12).Children(func() {
		ui.Text(c, fmt.Sprint(s.n)).FontSize(40).Bold()
		if ui.PrimaryButton(c, "Increment").Clicked() {
			s.n++
		}
	})
}

func main() {
	s := &counter{}
	mygo.App.WhenReady(func() {
		mygo.NewWindow(mygo.WindowOptions{
			Title:   "Counter",
			Width:   320,
			Height:  240,
			Content: ui.View(s.view),
		})
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
```

`mygo init -template native my-app` starts a project of native UI (see
[the CLI](cli.md#mygo-init)). The gallery example tours what the toolkit
does: `go run ./examples/gallery` in a clone of the repository.

One app can have windows of both kinds. Native UI suits tools, settings,
inspectors and utilities, and apps that must start instantly; a web page
suits rich documents, existing web code and anything that needs what only
a browser has. Screen readers and other assistive technology read native
UI as they read other apps (see [Accessibility](#accessibility)).

## Views

The view is a function from your app's state to its interface. MyGo calls
it on the main thread to build every frame: after input, after you change
the state (see [below](#change-the-state-from-other-goroutines)), and while
something animates. Elements live for one frame: the state that lasts is
yours, in your own types, plus what MyGo keeps for each element from frame
to frame (focus, hover, scrolling, the text being edited, animations).

Events are questions you ask while building: `Clicked` reports whether the
element was clicked since the last frame, so the code that handles a click
sits where the button is built:

```go
if ui.Button(c, "Delete").Clicked() {
	app.items = slices.Delete(app.items, i, i+1)
}
```

When a handler changes the state while the view builds, MyGo builds the
frame again, so it always shows the outcome.

MyGo tells elements apart by their position among their siblings. When the
siblings before an element can change, as in a list whose items you
insert, delete or reorder, give each item a `Key` so its state follows it:

```go
for i := range app.todos {
	todo := &app.todos[i]
	ui.Row(c).Key(todo.ID).Children(func() {
		ui.Checkbox(c, &todo.Done, todo.Title)
	})
}
```

Widgets that handle their input as they are created, such as `Checkbox`
here, take the key from an element around them: `Key` panics on them, as
their state would be lost.

### Change the state from other goroutines

The view reads your state on the main thread. Change it from other
goroutines with `Window.Update`, which runs a function on the main thread
and then draws a new frame:

```go
go func() {
	items, err := fetchItems()
	win.Update(func() { app.items, app.err = items, err })
}()
```

`Window.Invalidate` only draws a new frame, for state you guard yourself.
Within the view, `c.Invalidate()` asks for another frame and `c.After(d)` for
one after a delay, such as a clock's next second.

## Layout

Elements lay out their children with flexbox, as in CSS, in
device-independent pixels (DIPs):

- `ui.Column` stacks its children from top to bottom and stretches them to
  its width; `ui.Box` is a column too. `ui.Row` places them from left to
  right and centers them vertically.
- `Width`, `Height` and `Size` set sizes; `WidthPercent` and `HeightPercent`
  take a share of the parent; `MinWidth`, `MaxWidth`, `MinHeight` and
  `MaxHeight` bound them; `Fill`, `FillWidth` and `FillHeight` take the
  parent's whole content box.
- `Grow(1)` gives an element the free space along its parent's direction,
  like `flex: 1`: a list that fills the rest of a window, or a `Spacer` that
  pushes the elements after it to the end. `Shrink` and `Basis` work as in
  CSS.
- `Gap` spaces the children; `Padding` and `Margin` take one, two or four
  values, as in CSS.
- `Justify` places the children along the direction (`Start`, `Center`,
  `End`, `SpaceBetween`, `SpaceAround`, `SpaceEvenly`), `AlignItems` across
  it (`Start`, `Center`, `End`, `Stretch`), `AlignSelf` one child; `Center`
  centers them both ways, and `Wrap` wraps a row onto more lines.
- `Absolute` takes an element out of the flow, placed with `Top`, `Right`,
  `Bottom` and `Left` in its parent; `AspectRatio` keeps its proportions;
  `Clip` cuts its children to its rounded box.

`ui.Scroll` and `ui.ScrollHorizontal` scroll their children with the wheel,
the touchpad, their scroll bar and the keyboard: the arrow keys, Page Up
and Page Down, Space, Home and End scroll the container around the focus,
or under the pointer, unless the focused element takes those keys, as a
text input does. Give them a size, or grow them in their parent. `ui.List` builds only the rows in view, so it shows
millions of rows of a fixed height as fast as ten:

```go
ui.List(c, len(app.rows), 32, func(i int) {
	ui.Text(c, app.rows[i].Name)
}).Grow(1)
```

## Text

`ui.Text` shows text that wraps at the width it gets, and `ui.Textf` formats
it. `FontSize`, `FontWeight`, `Bold`, `Italic`, `Font`, `LineHeight`,
`TextColor`, `TextAlign`, `Underline`, `Strikethrough`, `LetterSpacing`
and `FontFeatures` style it, set on the text or on any element above it,
whose texts inherit them. `FontFeatures` turns on OpenType features of the
font by tag, or sets them with `tag=value`: `FontFeatures("tnum")` gives
digits of one width for numbers that change, `FontFeatures("liga=0")`
turns ligatures off. `SingleLine`
keeps text on one line, cut with an ellipsis, and `MaxLines` limits it to a
few. `Selectable` lets the user select a text with the pointer, Shift and
the arrows, and copy it, as an error message or an identifier to paste
elsewhere.

`ui.RichText` mixes styles in one paragraph: each `ui.Span` sets what it
changes (font, size, weight, italics, color, underline, strikethrough,
letter spacing, features) over the style of the text, and the spans wrap
together:

```go
ui.RichText(c,
	ui.Span{Text: "Saved "},
	ui.Span{Text: "report.pdf", Weight: 600},
	ui.Span{Text: " to "},
	ui.Span{Text: "Documents", Color: t.Accent, Underline: true},
)
```

Text is laid out and drawn by the system's own text engine (DirectWrite on
Windows, Core Text on macOS, Pango on Linux) in the system's font (Segoe UI,
SF, the desktop's interface font, as GTK apps have it), falling back to the
system's fonts for other scripts and emoji as native apps do, with
right-to-left text in its order. `Font("monospace")` picks the system's
monospaced font, and `ui.RegisterFont` adds your own:

```go
//go:embed Inter.ttf
var inter []byte

func init() {
	if err := ui.RegisterFont(inter, "Inter"); err != nil {
		log.Fatal(err)
	}
}
```

Then `Font("Inter")` uses it, or set it for every element in the theme's
`Font`. A list of families, as `Font("Inter, Noto Sans JP")`, draws with
the first the system or the app has, and what it lacks with the next that
has it, before the system's own choice.

## Styling and themes

`Background`, `Gradient`, `Border`, `Radius`, `Shadow` and `Opacity` style
an element's box, and `Cursor` sets the pointer over it.

Widgets take their colors and metrics from the theme, `c.Theme()`: the light
or the dark theme, following the system's appearance as it changes. Use its
colors in your own elements so they follow too. To change it, set a copy:

```go
t := *ui.LightTheme()
if c.Theme().Dark {
	t = *ui.DarkTheme()
}
t.Accent, t.Radius = ui.Hex("#7c3aed"), 8
c.SetTheme(&t)
```

## Widgets

| | |
|---|---|
| `Button`, `PrimaryButton` | a push button; `Clicked` reports presses by the pointer, Enter or Space |
| `Link` | text that opens a URL in the browser |
| `Checkbox`, `Switch` | toggle a `*bool` |
| `Radio` | sets a `*T` to its value |
| `Select` | picks one of a list of strings, from a popup |
| `Slider` | sets a `*float64` within a range, by dragging or with the arrow keys |
| `Progress` | a bar filled from 0 to 1, or sliding across for a negative value, for work of unknown length |
| `TextInput`, `TextArea` | edit a `*string` on one line or several, with selection, undo, the clipboard and input methods; `Placeholder`, `Password`, `Submitted` (Enter) and `Changed` |
| `NumberInput` | edits a `*float64` within a range, typed or stepped with Up, Down and its buttons |
| `DateInput` | edits a `*time.Time` with a calendar, by click or with the arrow keys and Page Up and Down |
| `Tabs` | a row of tabs choosing a `*int`, by click or with the arrow keys |
| `Split`, `SplitVertical` | two panes with a divider between them that the user drags, or moves with the arrow keys, to resize them; the first's size is a `*float32` |
| `Table` | rows under a header of `TableColumn`s, built only while in view, choosing a `*int` by click or with Up and Down; a double click or Enter reports `Submitted` |
| `Tree`, `TreeItem` | items that open and close, built inside the items they belong to, with the arrow keys moving between them; `Clicked` and `Selected` choose one |
| `Image` | shows a `*ui.Bitmap` |
| `Divider`, `Spacer` | a line, and space that grows |
| `Scroll`, `ScrollHorizontal`, `List` | scroll containers, see [layout](#layout) |
| `Modal`, `Popover`, `Overlay` | dialogs and panels above the window, see [overlays](#overlays) |

Widgets that change a value take a pointer to it, so they need no handler:
`ui.Checkbox(c, &app.settings.Sync, "Sync")` changes the field the moment
the user clicks. `Changed` reports that they did, for work that follows:

```go
if ui.TextInput(c, &app.query).Placeholder("Search").Changed() {
	app.results = search(app.query)
}
```

Build your own widgets from elements. An element keeps state of its own
from frame to frame with `ui.Local`:

```go
func Disclosure(c *ui.Context, title string, body func()) {
	box := ui.Column(c)
	open := ui.Local(box, "open", func() bool { return false })
	box.Children(func() {
		head := ui.Row(c).Gap(6).Cursor(ui.CursorPointer).Focusable()
		if head.Clicked() {
			*open = !*open
		}
		head.Children(func() {
			ui.Text(c, map[bool]string{true: "▾", false: "▸"}[*open])
			ui.Text(c, title).Bold()
		})
		if *open {
			body()
		}
	})
}
```

## Input

- **Pointer.** `Hovered`, `Pressed`, `Clicked`, `DoubleClicked`,
  `RightClicked`, `Dragged` (how far the pointer moved since the last frame
  while pressing the element) and `PointerPosition`. `PassThrough` lets the
  pointer through to what is below.
- **Keyboard focus.** `Focusable` elements take the focus when clicked, and
  Tab and Shift+Tab move it between them, with a focus ring when it moves
  by keyboard. `AutoFocus` gives an element the focus when it appears, such
  as the first field of a dialog, and `Focus` keeps it there while you call
  it; `Focused`, `FocusVisible` and `FocusWithin` report it. Enter and Space
  press a focused button.
- **Shortcuts.** `c.Shortcut(ui.Cmd, ui.KeyS)` reports a key pressed with
  exactly those modifiers anywhere in the window, and `Element.Shortcut`
  only while the element or one inside it has the focus, which comes first.
  `ui.Cmd` is Command on macOS and Ctrl elsewhere. Shortcuts of
  [menus](menus.md) still work, and the Edit menu's roles (cut, copy, paste,
  select all, undo, redo) act on the focused text input. A focused text
  input takes the editing keys of the platform first: on macOS, Option and
  Command with the arrows and Backspace, and Control with A, E, B, F, N, P,
  D, H and K, as in other Mac apps.
- **Input methods.** Text inputs take text composed with input methods,
  which see the text around the caret: macOS's press and hold replaces the
  letter it accents, Japanese input methods convert typed text again, and
  others predict from what comes before.
- **Files.** `DroppedFiles` returns the paths of the files dropped on the
  element from Finder, Explorer or a file manager, and `FileDragOver`
  reports files dragged over it, to show it would take them. The window
  takes files only over such elements, or anywhere when it has
  `OnFileDrop` listeners, which get the files no element takes, with where
  they were dropped:

  ```go
  zone := ui.Column(c).Size(260, 80).Border(1, t.Border)
  if files := zone.DroppedFiles(); files != nil {
  	app.files = files
  }
  if zone.FileDragOver() {
  	zone.Border(2, t.Accent)
  }
  ```
- **Context menus.** `ContextMenu` gives an element a menu of the system's,
  which opens where the element is right-clicked (Control-clicked on
  macOS), and below it when the menu key or Shift+F10 is pressed while it
  or one inside it has the focus. The function builds the items when the
  menu opens, and runs again in the frame after one was chosen, where its
  `Chosen` reports it:

  ```go
  row.ContextMenu(func(m *ui.Menu) {
  	if m.Item("Rename").Shortcut(0, ui.KeyF2).Chosen() {
  		app.renaming = i
  	}
  	if m.Item("Pinned").Checked(note.pinned).Chosen() {
  		note.pinned = !note.pinned
  	}
  	m.Submenu("Move to", func(m *ui.Menu) {
  		for _, f := range app.folders {
  			if m.Item(f.name).Chosen() {
  				app.move(i, f)
  			}
  		}
  	})
  	m.Separator()
  	if m.Item("Delete").Disabled(note.locked).Chosen() {
  		app.delete(i)
  	}
  })
  ```

  The innermost element with a menu gets the click. Text inputs have the
  editing commands of their platform's text fields, unless `ContextMenu`
  gives them another menu. `Shortcut` only shows a key: handle it with
  `Shortcut` on the context or an element.
- **Tooltips.** `Tooltip("…")` shows a tip once the pointer rests on the
  element.
- **Custom title bars.** In a `Frameless` window, `DragWindow` makes an
  element move the window, and a double click on it maximizes the window.

## Overlays

`ui.Modal` shows a dialog over a dimmed window while a `*bool` is true, and
`ui.Popover` a panel below an element, such as a menu; clicking outside them
or pressing Escape sets it to false:

```go
more := ui.Button(c, "More ▾")
if more.Clicked() {
	app.menu = !app.menu
}
ui.Popover(c, more, &app.menu, func() {
	if ui.Button(c, "Rename").Clicked() {
		app.menu, app.renaming = false, true
	}
})
ui.Modal(c, &app.renaming, func() {
	ui.Text(c, "Rename").Bold()
	if ui.TextInput(c, &app.name).AutoFocus().Submitted() {
		app.renaming = false
	}
})
```

`c.Toast("Saved")` shows a message near the bottom of the window for a few
seconds, as the outcome of what the user just did; screen readers see it
as a status. `ui.Overlay` builds elements above everything else, placed
with `Absolute` in DIPs of the window. Native [dialogs](native.md#dialogs) work too: call
them from a goroutine, so that the view does not wait for them.

## Accessibility

Assistive technology, such as VoiceOver on macOS, Orca on Linux and
Narrator or NVDA on Windows, reads a window's native UI as it reads other
apps: the widgets with their roles, names, values and states, the texts,
and the elements that take the focus, as the content changes. It acts on
them as the keyboard and the pointer would: it presses buttons, checks
boxes, moves sliders, edits text inputs and moves the focus.

Widgets describe themselves, and the text inside an element names it, as
a button's does. Elements without text need a `Label`, which also finds
them in [tests](#testing):

```go
ui.Slider(c, &app.volume, 0, 100).Label("Volume")
ui.TextInput(c, &app.query).Placeholder("Search").Label("Search")
ui.Box(c).Size(24, 24).Draw(drawIcon).Label("Unread messages")
```

Other elements get a role from what they do: one that is clickable and
takes the focus is a button, a scroll container a scroll area, and one
with a `Label`, or that takes the focus, a group. `Role` sets it for an
element drawn as a widget it is not built from, and `ui.RoleNone` leaves
an element out but not its children:

```go
toggle := ui.Box(c).Size(36, 20).Focusable().Role(ui.RoleSwitch).Label("Wi-Fi")
```

MyGo describes frames only once assistive technology asked, so apps pay
nothing for it otherwise.

## Drawing and animation

`Draw` paints on an element after its background, and `DrawOver` after its
children, with a `*ui.Painter` in DIPs of the window: rectangles with
`Fill` and `Stroke`, `Shadow`, `Line`, `Text`, `Image`, `Clip`, and paths of
lines and curves with `FillPath` and `StrokePath`:

```go
ui.Box(c).Height(120).Draw(func(p *ui.Painter, r ui.Rect) {
	var wave ui.Path
	for i := 0; i <= 100; i++ {
		x := r.X + r.W*float32(i)/100
		y := r.Y + r.H/2 + 40*float32(math.Sin(float64(i)/8))
		if i == 0 {
			wave.MoveTo(x, y)
		} else {
			wave.LineTo(x, y)
		}
	}
	p.StrokePath(&wave, 2, c.Theme().Accent)
})
```

Draw functions only paint: MyGo may call them more than once a frame.

For motion, `Element.Animate` returns a value that eases to a target and
draws frames until it gets there:

```go
panel := ui.Column(c).Clip()
width := float32(0)
if app.sidebar {
	width = 280
}
panel.Width(panel.Animate("width", width, 200*time.Millisecond))
```

To animate continuously, compute from `c.Now()` and call
`c.AnimationFrame()` in every frame that moves: MyGo draws the next frame
when the display can show it, and draws nothing while nothing changes.

## Images

`ui.NewBitmap` makes a bitmap of an `image.Image`, and `ui.DecodeBitmap` of
PNG, JPEG or GIF data. Make bitmaps once, not in the view: MyGo keeps a
bitmap on the GPU as long as you use it.

```go
//go:embed logo.png
var logoPNG []byte

var logo, _ = ui.DecodeBitmap(logoPNG)

ui.Image(c, logo).Size(64, 64).Fit(ui.Contain).Radius(12)
```

## Windows with native UI

A window with `Content` takes the [window options](windows.md#options) of
any window, such as its size, `StateKey`, `Frameless` and `Parent`, as well
as menus, dialogs and the other native APIs. It has no page: it ignores
`URL` and the options of pages, and the methods of pages do nothing or fail
(`Eval` returns an error). Its Go code needs no bindings: the view calls it
directly. `CapturePage` returns a PNG of what it shows.

With `TitleBarStyle: mygo.TitleBarHidden`, the view draws the title bar
under the window controls, as a page does with the `--mygo-titlebar-*` CSS
variables: `c.TitleBar()` returns the room the controls take, zero in full
screen, and `DragWindow` makes elements drag the window, which
double-clicking them zooms or minimizes as a title bar would:

```go
bar := c.TitleBar()
ui.Row(c).Height(max(bar.Height, 32)).Padding(0, bar.Right+12, 0, bar.Left+12).DragWindow().Children(func() {
	ui.Text(c, "Inbox").Bold()
})
```

On macOS, a window's `Vibrancy` shows wherever its native UI draws no
background. The root draws the theme's by default: make it transparent and
give backgrounds to the parts that need one, as a sidebar beside opaque
content does:

```go
c.Root().Background(ui.Transparent)
ui.Row(c).Fill().AlignItems(ui.Stretch).Children(func() {
	app.sidebar(c) // over the material
	ui.Column(c).Grow(1).Background(c.Theme().Background).Children(func() { app.content(c) })
})
```

On Linux, an app whose windows all show native UI needs GTK 3 alone, not
WebKitGTK.

## Testing

`ui.NewTester` runs a view without a window, as fast as a unit test: it
renders frames in memory, finds elements by their text, and clicks, types,
scrolls and presses keys.

```go
func TestCounter(t *testing.T) {
	s := &counter{}
	tt := ui.NewTester(s.view, 320, 240)
	if err := tt.Click("Increment"); err != nil {
		t.Fatal(err)
	}
	if s.n != 1 || !tt.HasText("1") {
		t.Errorf("count %d, texts %q", s.n, tt.Texts())
	}
}
```

`tt.RightClick` opens a context menu, which `tt.Menu` lists and
`tt.ChooseMenuItem("Move to", "Archive")` chooses from. `tt.Image()` is the
last frame, for snapshots, and `ui.Render` draws a view once at a given
scale.

## Rendering

MyGo draws on the GPU with Metal on macOS, with Direct3D 11 on Windows,
or with WARP, Windows' own software renderer, where no GPU driver works,
and with OpenGL on Linux, in the GtkGLArea GTK shows. A shader computes
rounded rectangles, borders, gradients and shadows from the distance to
their edges, so they stay sharp at any size and scale, and text comes
from a glyph atlas that only uploads what changes.

Where OpenGL would not run on a GPU, as in virtual machines or in WSL
(where `GALLIUM_DRIVER=d3d12` gives Mesa the GPU), Linux draws the same
pixels on the CPU: a few milliseconds for a whole large window on a
high-density display, and less than a tenth of one for what typically
changes, such as a button under the pointer, since it redraws only that.
Set `MYGO_GPU=0` to use the CPU renderer everywhere, for instance to
compare, and on Linux `MYGO_GPU=1` to draw with OpenGL even where it runs
on the CPU.
