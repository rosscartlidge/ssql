#!/bin/sh
# Hybrid BM25+embedding search over the project docs/journals.
# Usage: scripts/docsearch.sh [-k N] [-lexical] QUERY...
exec go -C "$(dirname "$0")/docsearch" run . "$@"
