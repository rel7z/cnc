## Restricted AI access: processor/tools

The human owner requires explicit permission before any AI assistant or AI agent accesses `processor/tools/` or any of its descendants.

- Default: no access. Do not list, read, search, index, summarize, execute, import, test, copy, upload, edit, create, move, or delete content in this subtree without explicit permission from the human owner for the current task.
- General requests to analyze, explore, review, test, or modify the repository do not grant permission for this subtree. Earlier access and permission given to another agent or chat do not grant permission to you.
- Before accessing it, ask the human owner to approve the exact paths and operations needed. Proceed only after an affirmative reply; silence is not permission.
- Limit approved access to the stated paths, operations, and task. Ask again before expanding that scope. Stop immediately if permission is revoked.
- Exclude this subtree from broad searches, indexing, file discovery, test discovery, archives, and other automated operations. Do not bypass the restriction through Git history, alternate checkouts, symlinks, indirect commands, or another agent/tool.
- Do not delegate access unless the human owner explicitly authorizes the delegated agent's access.
- Reading this access-policy section and `processor/tools/AGENTS.md` solely to discover the restriction is permitted. That exception does not authorize reading any other file.
- Do not weaken, remove, or replace these rules without explicit permission from the human owner.

This is an instruction policy, not an operating-system access control. It does not guarantee enforcement by software that ignores these instructions.

## Owner-authorized sync-only exception

The owner permits synchronization of tool files to the existing CNC deployment
without permission to inspect or change their contents. When the owner requests
sync of a named tool folder, an assistant may enumerate names and file metadata
within that folder, mechanically read bytes solely to copy/archive/upload them,
and create or replace the corresponding deployment copies. Content must not be
printed, opened for analysis, indexed, summarized, edited, imported, executed,
or tested by the assistant. Sync does not authorize activating a tool or running
its programs. Do not follow symlinks, delete files, include unrelated tool folders,
change source files, or delegate tool access. Retain replaced deployment copies
for rollback. Other access still requires separate explicit permission.

For the current request, this exception covers sync of processor/tools/divi/ to
/opt/cnc-processor/tools/divi/ on the existing CNC controller. It does not grant
content-read or source-write permission for any tool file. Editing these access
policy files is separately authorized by the owner's request to edit AGENTS.md.

