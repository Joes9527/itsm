#!/bin/sh
set -eu

# Fail without echoing the matching line or credential-shaped value. This is a
# narrow repository gate; external rotation remains the credential owner's
# responsibility.
if git grep -I -q -E \
  '(^|[^A-Za-z0-9])(sk-[A-Za-z0-9_-]{20,}|AKIA[0-9A-Z]{16}|gh[pousr]_[A-Za-z0-9]{20,})' -- .; then
  echo "credential-shaped value detected in tracked tree" >&2
  exit 1
fi

echo "tracked-tree credential-shape scan passed"
