#!/bin/bash
# TXN2 Go MCP Server Template Setup Script
#
# Usage: ./setup.sh <project-name> <project-description> <github-org> <maintainer-github> <maintainer-email> <docs-url>
#
# Example:
#   ./setup.sh mcp-foo "MCP server for Foo" txn2 cjimti cj@imti.co mcp-foo.txn2.com
#
# This script replaces all template variables with your project-specific values.

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Print colored message
info() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

error() {
    echo -e "${RED}[ERROR]${NC} $1"
    exit 1
}

# Check arguments
if [ $# -lt 6 ]; then
    echo "Usage: $0 <project-name> <project-description> <github-org> <maintainer-github> <maintainer-email> <docs-url>"
    echo ""
    echo "Arguments:"
    echo "  project-name        - Project binary/module name (e.g., mcp-foo)"
    echo "  project-description - One-line description (e.g., \"MCP server for Foo\")"
    echo "  github-org          - GitHub organization (e.g., txn2)"
    echo "  maintainer-github   - Maintainer GitHub username (e.g., cjimti)"
    echo "  maintainer-email    - Maintainer email (e.g., cj@imti.co)"
    echo "  docs-url            - Documentation site URL (e.g., mcp-foo.txn2.com)"
    echo ""
    echo "Example:"
    echo "  $0 mcp-foo \"MCP server for Foo\" txn2 cjimti cj@imti.co mcp-foo.txn2.com"
    exit 1
fi

PROJECT_NAME="$1"
PROJECT_DESCRIPTION="$2"
GITHUB_ORG="$3"
MAINTAINER_GITHUB="$4"
MAINTAINER_EMAIL="$5"
DOCS_URL="$6"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

info "Setting up project: $PROJECT_NAME"
info "Description: $PROJECT_DESCRIPTION"
info "GitHub: $GITHUB_ORG/$PROJECT_NAME"
info "Maintainer: @$MAINTAINER_GITHUB <$MAINTAINER_EMAIL>"
info "Docs URL: $DOCS_URL"
echo ""

# Function to replace template variables in files
replace_vars() {
    local file="$1"
    if [ -f "$file" ]; then
        # Use different sed syntax based on OS
        if [[ "$OSTYPE" == "darwin"* ]]; then
            sed -i '' \
                -e "s/{{project-name}}/$PROJECT_NAME/g" \
                -e "s/{{project-description}}/$PROJECT_DESCRIPTION/g" \
                -e "s/{{github-org}}/$GITHUB_ORG/g" \
                -e "s/{{maintainer-github}}/$MAINTAINER_GITHUB/g" \
                -e "s/{{maintainer-email}}/$MAINTAINER_EMAIL/g" \
                -e "s/{{docs-url}}/$DOCS_URL/g" \
                "$file"
        else
            sed -i \
                -e "s/{{project-name}}/$PROJECT_NAME/g" \
                -e "s/{{project-description}}/$PROJECT_DESCRIPTION/g" \
                -e "s/{{github-org}}/$GITHUB_ORG/g" \
                -e "s/{{maintainer-github}}/$MAINTAINER_GITHUB/g" \
                -e "s/{{maintainer-email}}/$MAINTAINER_EMAIL/g" \
                -e "s/{{docs-url}}/$DOCS_URL/g" \
                "$file"
        fi
    fi
}

# Find all files and replace variables
info "Replacing template variables in files..."
find "$SCRIPT_DIR" -type f \( \
    -name "*.go" \
    -o -name "*.mod" \
    -o -name "*.yml" \
    -o -name "*.yaml" \
    -o -name "*.json" \
    -o -name "*.md" \
    -o -name "*.css" \
    -o -name "Makefile" \
    -o -name "Dockerfile" \
    -o -name ".gitignore" \
    -o -name "CODEOWNERS" \
    -o -name "*.sh" \
\) -not -path "*/.git/*" | while read -r file; do
    replace_vars "$file"
    echo "  Processed: $file"
done

# Rename the cmd directory
if [ -d "$SCRIPT_DIR/cmd/{{project-name}}" ]; then
    info "Renaming cmd/{{project-name}} to cmd/$PROJECT_NAME..."
    mv "$SCRIPT_DIR/cmd/{{project-name}}" "$SCRIPT_DIR/cmd/$PROJECT_NAME"
fi

# Remove this setup script (optional - uncomment if desired)
# info "Removing setup script..."
# rm "$SCRIPT_DIR/setup.sh"

# Initialize git if not already initialized
if [ ! -d "$SCRIPT_DIR/.git" ]; then
    read -p "Initialize git repository? [y/N] " -n 1 -r
    echo
    if [[ $REPLY =~ ^[Yy]$ ]]; then
        info "Initializing git repository..."
        cd "$SCRIPT_DIR"
        git init
        git add .
        git commit -m "Initial commit from template-go-mcp

Generated from https://github.com/txn2/template-go-mcp"
        info "Git repository initialized with initial commit"
    fi
fi

echo ""
info "Setup complete!"
echo ""
echo "Next steps:"
echo "  1. Review the generated files"
echo "  2. Add a logo.png to docs/images/"
echo "  3. Update go.mod dependencies: go mod tidy"
echo "  4. Run tests: make test"
echo "  5. Run linter: make lint"
echo "  6. Create GitHub repository: gh repo create $GITHUB_ORG/$PROJECT_NAME --public"
echo "  7. Push code: git remote add origin git@github.com:$GITHUB_ORG/$PROJECT_NAME.git && git push -u origin main"
echo ""
echo "Repository setup checklist:"
echo "  - [ ] Enable GitHub Pages (Settings > Pages > Source: GitHub Actions)"
echo "  - [ ] Add secrets: HOMEBREW_TAP_TOKEN, CODECOV_TOKEN"
echo "  - [ ] Enable Dependabot (Settings > Security > Dependabot alerts)"
echo "  - [ ] Configure branch protection for main branch"
echo ""
