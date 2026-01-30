#!/bin/bash
# Auto-increment version based on number of changed lines
# 10-50 lines: patch, 51-400 lines: minor, 401+ lines: major

set -e

VERSION_FILE="main.go"

# Get current version
CURRENT=$(grep 'const Version = ' "$VERSION_FILE" | sed 's/.*"\(.*\)".*/\1/')
if [ -z "$CURRENT" ]; then
    echo "Could not find version in $VERSION_FILE"
    exit 1
fi

# Parse version components
IFS='.' read -r MAJOR MINOR PATCH <<< "$CURRENT"

# Count changed lines - try different methods
get_line_count() {
    local stat_line="$1"
    local insertions=$(echo "$stat_line" | grep -oE '[0-9]+ insertion' | grep -oE '[0-9]+' || echo "0")
    local deletions=$(echo "$stat_line" | grep -oE '[0-9]+ deletion' | grep -oE '[0-9]+' || echo "0")
    echo $((${insertions:-0} + ${deletions:-0}))
}

# Try staged + unstaged against HEAD
STAT=$(git diff HEAD --stat 2>/dev/null | tail -1)
LINES=$(get_line_count "$STAT")

# If no changes, try just staged
if [ "$LINES" -eq 0 ]; then
    STAT=$(git diff --cached --stat 2>/dev/null | tail -1)
    LINES=$(get_line_count "$STAT")
fi

# If still no changes, try just working tree
if [ "$LINES" -eq 0 ]; then
    STAT=$(git diff --stat 2>/dev/null | tail -1)
    LINES=$(get_line_count "$STAT")
fi

echo "Current version: $CURRENT"
echo "Lines changed: $LINES"

# Determine bump type
if [ "$LINES" -lt 10 ]; then
    echo "Less than 10 lines changed, no version bump needed"
    exit 0
elif [ "$LINES" -le 50 ]; then
    BUMP="patch"
    PATCH=$((PATCH + 1))
elif [ "$LINES" -le 400 ]; then
    BUMP="minor"
    MINOR=$((MINOR + 1))
    PATCH=0
else
    BUMP="major"
    MAJOR=$((MAJOR + 1))
    MINOR=0
    PATCH=0
fi

NEW_VERSION="$MAJOR.$MINOR.$PATCH"
echo "Bump type: $BUMP"
echo "New version: $NEW_VERSION"

# Update version in file
sed -i '' "s/const Version = \"$CURRENT\"/const Version = \"$NEW_VERSION\"/" "$VERSION_FILE"

echo "Updated $VERSION_FILE"
