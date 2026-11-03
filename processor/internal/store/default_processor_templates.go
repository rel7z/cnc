package store

import (
	_ "embed"
	"encoding/json"
	"time"
)

// These are saved editor drafts only. Seeding never registers programs, reads
// tool directories, changes routes, enables processing or executes programs.
//
//go:embed default-processor-templates.json
var defaultProcessorTemplatesJSON []byte

func (s *Store) seedDefaultProcessorTemplates() error {
	var templates []ProcessorTemplate
	if err := json.Unmarshal(defaultProcessorTemplatesJSON, &templates); err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, item := range templates {
		added, err := tx.Exec("INSERT OR IGNORE INTO processor_template_seeds(id) VALUES(?)", item.ID)
		if err != nil {
			return err
		}
		n, err := added.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			continue
		}
		now := time.Now().UTC().Format(time.RFC3339Nano)
		if _, err := tx.Exec("INSERT OR IGNORE INTO processor_templates(id,name,definition,revision,created_at,updated_at) VALUES(?,?,?,?,?,?)", item.ID, item.Name, string(item.Definition), 1, now, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}
