// Package ui builds user interfaces that MyGo draws itself, on the GPU
// where it can, for windows that show no web page:
//
//	type counter struct{ n int }
//
//	func (s *counter) view(c *ui.Context) {
//		ui.Column(c).Fill().Center().Gap(12).Children(func() {
//			ui.Text(c, fmt.Sprint(s.n)).FontSize(40).Bold()
//			if ui.PrimaryButton(c, "Increment").Clicked() {
//				s.n++
//			}
//		})
//	}
//
//	mygo.NewWindow(mygo.WindowOptions{Title: "Counter", Content: ui.View(s.view)})
//
// A view is a function of the app's state that builds the interface of one
// frame on the main thread; MyGo calls it again after input, after
// Window.Update or Invalidate, and while something animates. Elements lay
// out their children with flexbox, take their colors from a Theme that
// follows the system's appearance, and answer questions about input, such
// as Clicked, where they are built. What an element keeps between frames
// (focus, scrolling, text being edited, animations) follows its position
// among its siblings, or its Key.
//
// Tester runs views in tests, without a window.
package ui
