package ui

// Tabs creates a row of tabs showing labels, of which *selected is the
// index of the one chosen. A click chooses a tab, as do the arrows, Home
// and End while one has the keyboard focus, which follows the choice:
//
//	ui.Tabs(c, &app.tab, "General", "Appearance", "Advanced")
//	switch app.tab {
//	case 0:
//		app.general(c)
//	…
//	}
//
// Changed reports a new choice.
func Tabs(c *Context, selected *int, labels ...string) *Element {
	t := c.theme
	list := Row(c).Gap(4).Shrink(0).Role(RoleTabList)
	list.widget = "Tabs"
	n := len(labels)
	if n == 0 {
		return list
	}
	*selected = max(0, min(*selected, n-1))
	// follow is set when the keys chose a tab, which then takes the focus.
	follow := Local(list, "follow", func() bool { return false })
	choose := func(i int, keys bool) {
		i = (i%n + n) % n
		if i != *selected {
			*selected = i
			list.st.changed = true
			c.rt.consumed = true
		}
		*follow = keys
	}
	list.Children(func() {
		for i, label := range labels {
			tab := Row(c).Padding(8, 12).Focusable().Shrink(0).Role(RoleTab)
			tab.widget = "Tab"
			tab.flags |= flagClickable | flagHover | flagOwnRing
			if tab.Clicked() {
				choose(i, false)
			}
			switch {
			case tab.Shortcut(0, KeyRight), tab.Shortcut(0, KeyDown):
				choose(i+1, true)
			case tab.Shortcut(0, KeyLeft), tab.Shortcut(0, KeyUp):
				choose(i-1, true)
			case tab.Shortcut(0, KeyHome):
				choose(0, true)
			case tab.Shortcut(0, KeyEnd):
				choose(n-1, true)
			}
			on := i == *selected
			if on && *follow {
				tab.Focus()
				c.rt.focusVisible = true
				*follow = false
			}
			tab.checked = 1 + int8(b2f(on))
			tab.TextColor(t.TextMuted)
			if on {
				tab.TextColor(t.Text).FontWeight(600)
			}
			tab.styleFn = func(tab *Element) {
				if !on && tab.Hovered() {
					tab.ts.color = t.Text
				}
			}
			tab.DrawOver(func(p *Painter, r Rect) {
				if on {
					p.Fill(Rect{r.X + 6, r.Y + r.H - 2, r.W - 12, 2}, t.Accent, 1)
				}
				if tab.FocusVisible() {
					p.FocusRing(r, [4]float32{t.Radius, t.Radius, t.Radius, t.Radius})
				}
			})
			tab.Children(func() { Text(c, label).SingleLine() })
		}
	})
	return list
}
