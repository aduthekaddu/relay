---
title: Files
description: Browse, preview, edit, upload and download files on your machine from any browser, with Trash, storage usage and search.
---

**Files** is a file manager for your machine that works well on a phone.
You can browse any folder in your home directory, preview images, videos,
PDFs, Markdown and code, and edit text files. You can upload whole folders
with resumable transfers and download folders as a zip. Deleted items go
to a Trash you can restore from. From any folder, you can also open a
terminal, start an agent or open VS Code right there.

## Browse

- **Breadcrumbs** at the top show where you are. Tap any part of the path to
  jump to it.
- Switch between **list** and **grid** view. Grid shows image thumbnails.
- **Sort** by name, size or date modified, in either direction.
- **Hidden files** (names that start with a dot) are hidden by default. The
  toolbar shows how many there are. Select it to show them.
- In git repositories, changed files show their git status letter (`M`,
  `A`, `?`).
- On a computer, use the arrow keys to move, Enter to open, and Backspace
  to go up. Shift or ⌘/Ctrl select multiple items.
- On a phone, long-press an item to start selecting.

Relay only shows folders inside the **files root**, which is your home
folder by default. Symbolic links that point outside the root are refused.
To limit Files to one folder, set [`files.root`](../reference/configuration.md#files).

## Preview and open files

Tap a file to preview it:

| Type | Preview |
| --- | --- |
| Images (JPEG, PNG, GIF, WebP, SVG…) | Full view, with thumbnails in grid view |
| Video and audio | Played in the browser, with seeking |
| PDF | Page viewer |
| Markdown | Rendered, with a toggle to show the source |
| Code and text | Syntax highlighted |
| Anything else | File info and a **Download** button |

:::note[Why some files download instead of opening]
Files are shown in a sandbox, so a file cannot run code as if it were part
of Relay. HTML files, and other types that could run script, are offered as
downloads instead of being rendered. This protects your session from
malicious files, including files an agent downloaded.
:::

## Edit a text file

1. Open the file and select **Edit**.
2. Make your changes. The editor works with a phone keyboard, and has
   syntax highlighting, search and undo.
3. Select **Save** (⌘S / Ctrl+S).

If the file changed on disk while you were editing (for example, an agent
also edited it), Relay does not overwrite it. It shows a conflict and lets
you reload the file or keep your version. Text files up to 5 MB can be
edited in the browser. For bigger projects, use [Code](code.md).

## Upload files and folders

- **Drag and drop** files or whole folders onto the file list.
- Select **Upload** and pick files or a folder. On a phone, you can also
  pick from your photo library or take a photo.
- **Paste** an image or file while Files is open.

Uploads go into the folder you are viewing. Large files are sent in chunks:
if the connection drops, the upload resumes where it stopped. A progress
panel shows each file. The size limit is
[`terminal.upload_max_mb`](../reference/configuration.md#terminal)
(512 MB by default).

## Download

- **One file:** open it and select **Download**, or choose **Download** in
  the item's menu.
- **Folders or several items:** select them and choose **Download as zip**.
  The zip is streamed as it is built, so downloads start at once, even for
  big folders.

## Organise

| Action | How |
| --- | --- |
| New folder / new file | **New** in the toolbar |
| Rename | Item menu → **Rename**, or F2 on a computer |
| Move | Drag onto a folder or a breadcrumb, or item menu → **Move to…** |
| Copy | Item menu → **Copy to…**. Large copies run in the background with progress |
| Delete | Item menu → **Move to Trash** (⌘⌫ on a Mac, Delete on Windows/Linux) |

## Restore from the Trash

Deleted items go to the Trash, which follows the standard Linux desktop
layout (`~/.local/share/Trash`), so desktop file managers see the same
Trash.

1. Select **Trash** in the sidebar (on a phone, in the ⋯ menu).
2. Select items and choose **Restore**. They go back where they came from.
3. **Empty Trash** deletes everything in it for good (you confirm first).

To delete immediately instead of using the Trash, set
[`files.use_trash`](../reference/configuration.md#files) to `false`. Every
delete is recorded in the [activity log](security.md#check-the-activity-log).

## See what uses your disk

Select **Storage** (or **Folder sizes** in a folder's menu) to see how big
each folder is and which items are largest. The scan runs in the background
and stays on one disk. It does not follow links onto other disks or network
drives. Results are cached, and **Rescan** updates them.

## Search

Type in the search field at the top of Files:

- **By name:** results appear as you type. Folders like `node_modules`,
  `.git` and `.cache` are skipped to keep search fast.
- **By content:** switch to **Contents** to search inside files. This uses
  ripgrep (`rg`) when it is installed, which is very fast. Install ripgrep
  from the [Toolbox](toolbox.md) if it is missing.

You can also find files from the [command center](command-center.md).

## Work from a folder

Every folder's menu has:

- **Open terminal here**: a new terminal in that folder.
- **Start agent here**: opens the agent launchpad with this folder
  selected.
- **Open in Code**: opens the folder in [VS Code in the browser](code.md).

## Next steps

- [Code](code.md): edit whole projects in VS Code.
- [Terminal](terminal.md): paste images into a terminal as file paths.
- [Backup and migrate](backup-and-migrate.md)
