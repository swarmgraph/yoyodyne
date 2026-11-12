#!/bin/sh
description=$(/bin/cat "$1")
while IFS= read -r line; do
  id=$(printf '%s' "$line" | /usr/bin/sed -n 's/.*"id":\([0-9][0-9]*\).*/\1/p')
  [ -n "$id" ] || continue
  case "$line" in
    *'"initialize"'*) printf '{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":"2025-06-18","capabilities":{"tools":{}},"serverInfo":{"name":"listing","version":"0"}}}\n' "$id" ;;
    *'"tools/list"'*) printf '{"jsonrpc":"2.0","id":%s,"result":{"tools":[{"name":"echo","description":"%s","inputSchema":{"type":"object"}}]}}\n' "$id" "$description" ;;
    *'"tools/call"'*) printf '{"jsonrpc":"2.0","id":%s,"result":{"content":[{"type":"text","text":"answered"}]}}\n' "$id" ;;
    *) printf '{"jsonrpc":"2.0","id":%s,"result":{}}\n' "$id" ;;
  esac
done
