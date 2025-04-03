package main

import "database/sql"

func InsertSteps(db *sql.DB, steps []Steps) error {
	createSQL := `
	create table if not exists steps (
    	day TEXT NOT NULL PRIMARY KEY,
    	steps INTEGER NOT NULL
	);
	`
	_, err := db.Exec(createSQL)
	if err != nil {
		return err
	}

	// insert
	for _, step := range steps {
		insertSQL := `insert or replace into steps (day, steps) values (?, ?);`
		_, err = db.Exec(insertSQL, step.Day, step.Steps)
		if err != nil {
			return err
		}
	}
	return nil
}
