#!/bin/bash
# Runs inside the omloader-live image: prints "package<TAB>version<TAB>licenses" from dpkg + /usr/share/doc/<pkg>/copyright
dpkg-query -W -f='${Package}\t${Version}\n' | while IFS=$'\t' read -r p v; do
  f=/usr/share/doc/$p/copyright
  if [ -r "$f" ]; then l=$(grep -hE '^License:' "$f" | sed 's/^License: *//' | sort -u | tr '\n' ';'); else l=; fi
  printf '%s\t%s\t%s\n' "$p" "$v" "${l:-see /usr/share/doc/$p/copyright}"
done
