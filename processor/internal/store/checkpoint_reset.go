package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

var ErrResetBusy = errors.New("pause processing and finish or cancel queued/running jobs before resetting checkpoints")

type CheckpointReset struct {
	ID    string `json:"reset_id"`
	Count int    `json:"checkpoints_reset"`
}

// ResetCaptureCheckpoints preserves jobs, results, templates and a checkpoint
// backup. A new capture generation is created only after the owner resumes.
func (s *Store) ResetCaptureCheckpoints(ctx context.Context) (CheckpointReset, error) {
	s.scheduleMu.Lock()
	defer s.scheduleMu.Unlock()
	result := CheckpointReset{ID: time.Now().UTC().Format("20060102T150405.000000000Z")}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	var enabled, busy bool
	if err = tx.QueryRowContext(ctx, "SELECT COALESCE((SELECT enabled FROM system_state WHERE id=1),0)").Scan(&enabled); err != nil {
		return result, err
	}
	if err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM runs WHERE status IN ('queued','running')) OR EXISTS(SELECT 1 FROM processor_attempts WHERE phase IN ('assigned','running','uploading'))").Scan(&busy); err != nil {
		return result, err
	}
	if enabled || busy {
		return result, ErrResetBusy
	}
	rows, err := tx.QueryContext(ctx, "SELECT filename,identity,generation,offset,prefix_hash FROM capture_cursors")
	if err != nil {
		return result, err
	}
	cursors := []CaptureCursor{}
	for rows.Next() {
		var c CaptureCursor
		if err = rows.Scan(&c.Filename, &c.Identity, &c.Generation, &c.Offset, &c.PrefixHash); err != nil {
			rows.Close()
			return result, err
		}
		cursors = append(cursors, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return result, err
	}
	rows, err = tx.QueryContext(ctx, "SELECT filename,last_offset FROM cursors")
	if err != nil {
		return result, err
	}
	legacy := map[string]int64{}
	for rows.Next() {
		var name string
		var offset int64
		if err = rows.Scan(&name, &offset); err != nil {
			rows.Close()
			return result, err
		}
		legacy[name] = offset
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return result, err
	}
	raw, _ := json.Marshal(cursors)
	old, _ := json.Marshal(legacy)
	if _, err = tx.ExecContext(ctx, "INSERT INTO processor_checkpoint_resets(id,cursors_json,legacy_json) VALUES(?,?,?)", result.ID, string(raw), string(old)); err != nil {
		return result, err
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM capture_cursors"); err != nil {
		return result, err
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM cursors"); err != nil {
		return result, err
	}
	result.Count = len(cursors) + len(legacy)
	return result, tx.Commit()
}
