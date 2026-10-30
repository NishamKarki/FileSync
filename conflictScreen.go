// Author: Santosh Tripathee

package main

import (
	"fmt"
)

// Called by the screen (the Conflicts button) to get the conflicts that
// still need the user's decision. Wails makes every exported App method
// available to the frontend automatically.
func (app *App) GetConflicts() ([]ConflictRecord, error) {
	if app.db == nil {
		return nil, fmt.Errorf("metadata database is unavailable")
	}
	return ListOpenConflicts(app.db)
}
