package main

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// Exact deduplication grows on disk, rather than retaining millions of Go
// strings/maps. One connection bounds the page cache and serializes inserts.
type diskSet struct {
	db     *sql.DB
	insert *sql.Stmt
	dir    string
}

func newDiskSet(parent string) (*diskSet, error) {
	dir, err := os.MkdirTemp(parent, "subnet-dedup-")
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "seen.db"))
	if err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	s := &diskSet{db: db, dir: dir}
	// This disposable index is never reused after a crash. Disk/write failures
	// remain fatal; selected output lines have already been flushed to the file.
	for _, statement := range []string{"PRAGMA journal_mode=WAL", "PRAGMA synchronous=OFF", "PRAGMA cache_size=-8192", "PRAGMA mmap_size=0", "PRAGMA temp_store=FILE", "PRAGMA wal_autocheckpoint=256", "CREATE TABLE seen (kind INTEGER, value TEXT, PRIMARY KEY(kind,value)) WITHOUT ROWID"} {
		if _, err = db.Exec(statement); err != nil {
			s.Close()
			return nil, err
		}
	}
	s.insert, err = db.Prepare("INSERT OR IGNORE INTO seen(kind,value) VALUES(?,?)")
	if err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}
func (s *diskSet) Add(ctx context.Context, kind int, value string) (bool, error) {
	result, err := s.insert.ExecContext(ctx, kind, value)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count == 1, err
}
func (s *diskSet) Close() {
	if s.insert != nil {
		s.insert.Close()
	}
	s.db.Close()
	os.RemoveAll(s.dir)
}
