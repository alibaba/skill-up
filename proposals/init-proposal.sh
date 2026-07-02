#!/usr/bin/env bash

# Copyright 2025 Alibaba Group Holding Ltd.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# Helper to bootstrap a new skill-up Enhancement Proposal.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TEMPLATE="$SCRIPT_DIR/proposal-template.md.template"

# Valid status values
VALID_STATUSES="draft provisional implementable implementing implemented withdrawn rejected"

usage() {
    cat <<EOF
Usage: $(basename "$0") [OPTIONS] <title>

Create a new skill-up Enhancement Proposal

Arguments:
  title                 Proposal title (will appear in the document header)

Options:
  -s, --status STATUS   Initial status of the proposal (default: draft)
                        Valid: draft, provisional, implementable, implementing,
                               implemented, withdrawn, rejected
  -a, --author AUTHOR   Author(s) to attribute in the new proposal
  -o, --output PATH     Explicit path to write the new proposal
  -h, --help            Show this help message

Examples:
  $(basename "$0") "Multi-Turn Conversation Eval"
  $(basename "$0") --status provisional --author "@user" "New Feature"
EOF
}

slugify() {
    local title="$1"
    echo "$title" \
        | tr '[:upper:]' '[:lower:]' \
        | sed -E 's/[^a-z0-9 _-]//g' \
        | sed -E 's/[ _-]+/-/g' \
        | sed -E 's/^-+|-+$//g'
}

default_author() {
    local author
    author=$(git config user.name 2>/dev/null || true)
    if [[ -z "$author" ]]; then
        author=$(git config user.email 2>/dev/null || true)
    fi
    if [[ -z "$author" ]]; then
        author="${USER:-Unknown Author}"
    fi
    echo "$author"
}

next_sequence() {
    local highest=0
    for file in "$SCRIPT_DIR"/[0-9][0-9][0-9][0-9]-*.md; do
        [[ -f "$file" ]] || continue
        local basename
        basename=$(basename "$file")
        local num="${basename%%-*}"
        # Remove leading zeros for arithmetic
        num=$((10#$num))
        if (( num > highest )); then
            highest=$num
        fi
    done
    echo $((highest + 1))
}

validate_status() {
    local status="$1"
    for valid in $VALID_STATUSES; do
        if [[ "$status" == "$valid" ]]; then
            return 0
        fi
    done
    echo "Error: Invalid status '$status'" >&2
    echo "Valid statuses: $VALID_STATUSES" >&2
    exit 1
}

require_value() {
    local option="$1"
    if [[ $# -lt 2 || -z "${2:-}" ]]; then
        echo "Error: option $option requires an argument" >&2
        usage >&2
        exit 1
    fi
}

yaml_double_quote_escape() {
    local value="$1"
    value="${value//\\/\\\\}"
    value="${value//\"/\\\"}"
    printf '%s' "$value"
}

# Parse arguments
TITLE=""
STATUS="draft"
AUTHOR=""
OUTPUT=""

while [[ $# -gt 0 ]]; do
    case "$1" in
        -h|--help)
            usage
            exit 0
            ;;
        -s|--status)
            require_value "$1" "${2:-}"
            STATUS=$(printf '%s' "$2" | tr '[:upper:]' '[:lower:]')
            shift 2
            ;;
        -a|--author)
            require_value "$1" "${2:-}"
            AUTHOR="$2"
            shift 2
            ;;
        -o|--output)
            require_value "$1" "${2:-}"
            OUTPUT="$2"
            shift 2
            ;;
        -*)
            echo "Error: Unknown option $1" >&2
            usage >&2
            exit 1
            ;;
        *)
            if [[ -z "$TITLE" ]]; then
                TITLE="$1"
            else
                echo "Error: Unexpected argument '$1'" >&2
                usage >&2
                exit 1
            fi
            shift
            ;;
    esac
done

# Validate required arguments
if [[ -z "$TITLE" ]]; then
    echo "Error: title is required" >&2
    usage >&2
    exit 1
fi

# Validate status
validate_status "$STATUS"

# Set defaults
if [[ -z "$AUTHOR" ]]; then
    AUTHOR=$(default_author)
fi

DATE=$(date +%Y-%m-%d)
SLUG=$(slugify "$TITLE")
if [[ -z "$SLUG" ]]; then
    SLUG="proposal"
fi
YAML_TITLE=$(yaml_double_quote_escape "$TITLE")
YAML_AUTHOR=$(yaml_double_quote_escape "$AUTHOR")

# Determine destination
if [[ -z "$OUTPUT" ]]; then
    SEQ=$(next_sequence)
    PROPOSAL_ID=$(printf "%04d" "$SEQ")
    DESTINATION="$SCRIPT_DIR/${PROPOSAL_ID}-${SLUG}.md"

    # Ensure unique filename
    while [[ -f "$DESTINATION" ]]; do
        SEQ=$((SEQ + 1))
        PROPOSAL_ID=$(printf "%04d" "$SEQ")
        DESTINATION="$SCRIPT_DIR/${PROPOSAL_ID}-${SLUG}.md"
    done
else
    DESTINATION="$OUTPUT"
    DESTINATION_BASENAME=$(basename "$DESTINATION")
    if [[ "$DESTINATION_BASENAME" =~ ^([0-9]+)- ]]; then
        PROPOSAL_ID=$(printf "%04d" "$((10#${BASH_REMATCH[1]}))")
    else
        SEQ=$(next_sequence)
        PROPOSAL_ID=$(printf "%04d" "$SEQ")
    fi
fi

# Check if destination exists
if [[ -f "$DESTINATION" ]]; then
    echo "Refusing to overwrite existing proposal at $DESTINATION" >&2
    exit 1
fi

# Verify template exists
if [[ ! -f "$TEMPLATE" ]]; then
    echo "Error: Proposal template not found at $TEMPLATE" >&2
    exit 1
fi

# Render template using pure bash substitution (avoids sed escaping issues)
content=$(<"$TEMPLATE")
content="${content//\{\{title\}\}/$TITLE}"
content="${content//\{\{yaml_title\}\}/$YAML_TITLE}"
content="${content//\{\{yaml_author\}\}/$YAML_AUTHOR}"
content="${content//\{\{status_metadata\}\}/$STATUS}"
content="${content//\{\{date\}\}/$DATE}"
content="${content//\{\{proposal_id\}\}/$PROPOSAL_ID}"
mkdir -p "$(dirname "$DESTINATION")"
printf '%s\n' "$content" > "$DESTINATION"

echo "Created $DESTINATION"
