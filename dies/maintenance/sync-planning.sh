#!/bin/bash
# Sync .planning/ to ~/dev/repos/{name}/planning/ via symlink for Syncthing.
# Idempotent: skips if already correctly symlinked. Migrates real dirs to symlinks.

repo_name=$(basename "$(pwd)")
sync_base="$HOME/dev/repos"

# Sync one directory: create symlink from repo_target → synced_path.
# Prints a one-line status message. Returns: 0=changed, 2=no change, 1=error.
sync_dir() {
  local repo_target="$1"
  local synced_path="$2"

  # Already a symlink?
  if link_dest=$(readlink "$repo_target" 2>/dev/null); then
    if [ "$link_dest" = "$synced_path" ]; then
      # Correct symlink — ensure the target directory exists
      if [ ! -d "$synced_path" ]; then
        mkdir -p "$synced_path" || { echo "$repo_target: mkdir failed: $synced_path"; return 1; }
        echo "$repo_target: created missing target dir"
        return 0
      fi
      echo "$repo_target: already symlinked"
      return 2
    fi
    # Symlink points elsewhere — don't touch it
    echo "$repo_target: unexpected symlink target: $link_dest"
    return 2
  fi

  # Real directory with content — migrate files, then replace with symlink
  if [ -d "$repo_target" ]; then
    entry_count=$(find "$repo_target" -maxdepth 1 -mindepth 1 | wc -l | tr -d ' ')
    if [ "$entry_count" -gt 0 ]; then
      mkdir -p "$synced_path" || { echo "$repo_target: mkdir failed: $synced_path"; return 1; }
      find "$repo_target" -maxdepth 1 -mindepth 1 -exec mv {} "$synced_path/" \;
      rmdir "$repo_target" || { echo "$repo_target: rmdir failed"; return 1; }
      ln -s "$synced_path" "$repo_target" || { echo "$repo_target: symlink failed"; return 1; }
      echo "$repo_target: migrated $entry_count files → symlink"
      return 0
    fi
    # Empty directory — remove and replace with symlink
    rmdir "$repo_target" 2>/dev/null
  fi

  # Create symlink
  mkdir -p "$synced_path" || { echo "$repo_target: mkdir failed: $synced_path"; return 1; }
  ln -s "$synced_path" "$repo_target" || { echo "$repo_target: symlink failed"; return 1; }
  echo "$repo_target: created symlink"
  return 0
}

changed=0
failed=0

sync_dir ".planning" "$sync_base/$repo_name/planning"
rc=$?
[ "$rc" -eq 0 ] && changed=1
[ "$rc" -eq 1 ] && failed=1

# ichrisbirch: also sync stats/data → ~/dev/repos/ichrisbirch/stats/
if [ "$repo_name" = "ichrisbirch" ] && { [ -d "stats/data" ] || [ -L "stats/data" ]; }; then
  sync_dir "stats/data" "$sync_base/$repo_name/stats"
  rc=$?
  [ "$rc" -eq 0 ] && changed=1
  [ "$rc" -eq 1 ] && failed=1
fi

[ "$failed" -eq 1 ] && exit 1
[ "$changed" -eq 0 ] && exit 2
exit 0
