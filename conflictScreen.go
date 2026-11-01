// Author: Santosh Tripathee

package main

import "fmt"

// GetConflicts lets the frontend request all unresolved conflicts.
func (app *App) GetConflicts() ([]ConflictRecord, error) {
	if app.db == nil {
		return nil, fmt.Errorf("metadata database is unavailable")
	}
	return ListOpenConflicts(app.db)
}
