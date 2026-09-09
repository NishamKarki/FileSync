package main

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
)

func main() {
	a := app.New()
	w := a.NewWindow("FileSync")
	w.Resize(fyne.NewSize(700, 550))

	w.ShowAndRun()
}
