package main

import (
	"context"

	"github.com/egoist/mygo"
)

// View is what the page of a window does for Go: the menu asks it what it
// shows, and tells it what to do.
type View struct {
	// Filter returns the filter the page shows.
	Filter func(ctx context.Context) (Filter, error)
	// FocusNew moves the focus to the field of a new todo.
	FocusNew func(ctx context.Context) error
}

// view calls the functions that the page of a window exposes (src/main.ts).
var view = mygo.NewPageAPI[View]()
