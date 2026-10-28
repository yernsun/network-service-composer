#!/bin/sh
set -eu
current=$(git config --get core.hooksPath || true)
if [ "$current" = .githooks ]; then
  echo 'NSC privacy hooks are already installed.'
  exit 0
fi
if [ -n "$current" ]; then
  echo 'An existing hooks path needs manual integration; it was preserved.' >&2
  exit 1
fi
for name in pre-commit pre-push; do
  hook=$(git rev-parse --git-path "hooks/$name")
  if [ -x "$hook" ]; then
    echo 'An existing commit/push hook needs manual integration; it was preserved.' >&2
    exit 1
  fi
done
git config --local core.hooksPath .githooks
echo 'Installed NSC privacy checks for this checkout.'
