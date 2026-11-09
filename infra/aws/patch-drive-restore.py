#!/usr/bin/env python3
"""Apply only the restore integration points to the existing controller source."""
from pathlib import Path
import sys
import re


def apply(root):
    path=root/'cnc-api/gdrive.go';source=path.read_text()
    if re.search(r'\brestoreStatus\s+DriveRestoreStatus', source):
        if '/api/config/gdrive/restore' not in (root/'cnc-api/server.go').read_text():
            raise RuntimeError('Incomplete previous restore patch')
        return
    def replace(old,new):
        nonlocal source
        if new in source:return
        if source.count(old)!=1:raise RuntimeError('Unexpected Drive source; patch refused: '+old[:60])
        source=source.replace(old,new,1)
    replace('type GDriveUploader struct {','type GDriveUploader struct {\n\tsyncMu sync.Mutex\n\trestoreMu sync.Mutex\n\trestoreStatus DriveRestoreStatus\n\trestoreCancel context.CancelFunc\n\trestored map[string]string')
    replace('func (u *GDriveUploader) Disconnect() error {','func (u *GDriveUploader) Disconnect() error {\n\tu.restoreMu.Lock()\n\tif u.restoreCancel != nil { u.restoreCancel() }\n\tu.restoreMu.Unlock()')
    replace('func (u *GDriveUploader) scanAndUpload() {','func (u *GDriveUploader) scanAndUpload() {\n\tu.syncMu.Lock()\n\tdefer u.syncMu.Unlock()')
    replace('alreadyUploaded := u.uploaded[stateKey]','restoredHash := u.restored[entry.Name()]\n\t\talreadyUploaded := u.uploaded[stateKey]')
    replace('\t\tif alreadyUploaded {','\t\tif restoredHash == contentHash { continue }\n\t\tif alreadyUploaded {')
    replace('\tfor _, name := range names {','\tfor _, name := range names {\n\t\tu.uploadedMu.RLock()\n\t\t_, restored := u.restored[name]\n\t\tu.uploadedMu.RUnlock()\n\t\tif restored { continue }')
    replace('type persistedGDriveState struct {','type persistedGDriveState struct {\n\tRestored map[string]string `json:"restored,omitempty"`')
    replace('\tfor _, h := range state.Uploaded {','\tu.restored = state.Restored\n\tfor _, h := range state.Uploaded {')
    replace('state := persistedGDriveState{Version: 1}','state := persistedGDriveState{Version: 1, Restored: make(map[string]string)}\n\tfor name, digest := range u.restored { state.Restored[name] = digest }')
    path.write_text(source)
    path=root/'cnc-api/server.go';source=path.read_text()
    old='mux.HandleFunc("/api/config/gdrive/sync", s.handleGDriveSync)'
    new=old+'\n\tmux.HandleFunc("/api/config/gdrive/restore", s.handleGDriveRestore)'
    if new not in source:
        if source.count(old)!=1:raise RuntimeError('Unexpected server route source')
        path.write_text(source.replace(old,new,1))

if __name__=='__main__':apply(Path(sys.argv[1]))
