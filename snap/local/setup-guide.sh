#!/bin/bash

# Define colors for better readability
BOLD="\033[1m"
GREEN="\033[32m"
BLUE="\033[34m"
YELLOW="\033[33m"
RESET="\033[0m"

echo -e "${BOLD}${GREEN}=== Devpack for Go: Setup Guide ===${RESET}\n"

# --- SECTION 1: ALIASES ---
echo -e "${BOLD}${BLUE}[1] Enable Direct Commands (Aliases)${RESET}"
echo "To run tools directly (e.g., 'dlv' instead of 'devpack-for-go.dlv'),"
echo "run the following commands in your terminal:"
echo ""
echo -e "${YELLOW}sudo snap alias devpack-for-go.delve dlv${RESET}"
echo -e "${YELLOW}sudo snap alias devpack-for-go.gopls gopls${RESET}"
echo -e "${YELLOW}sudo snap alias devpack-for-go.golangci-lint golangci-lint${RESET}"
echo -e "${YELLOW}sudo snap alias devpack-for-go.govulncheck govulncheck${RESET}"
echo -e "${YELLOW}sudo snap alias devpack-for-go.gotests gotests${RESET}"
echo -e "${YELLOW}sudo snap alias devpack-for-go.gofumpt gofumpt${RESET}"
echo ""

# --- SECTION 2: VS CODE CONFIG ---
echo -e "${BOLD}${BLUE}[2] VS Code Configuration${RESET}"
echo "To integrate these tools into VS Code, update your settings."
echo "Copy the block below into your 'settings.json'."
echo "  (Usually found at: ~/.config/Code/User/settings.json)"
echo ""
echo -e "${YELLOW}---------------- COPY BELOW ----------------${RESET}"
cat <<EOF
{
  "go.alternateTools": {
    "dlv": "/snap/bin/devpack-for-go.delve",
    "gopls": "/snap/bin/devpack-for-go.gopls",
    "golangci-lint": "/snap/bin/devpack-for-go.golangci-lint",
    "govulncheck": "/snap/bin/devpack-for-go.govulncheck",
    "gotests": "/snap/bin/devpack-for-go.gotests",
    "gofumpt": "/snap/bin/devpack-for-go.gofumpt"
  },
  "go.useLanguageServer": true,
  "go.lintTool": "golangci-lint",
  "go.formatTool": "gofumpt"
}
EOF
echo -e "${YELLOW}--------------------------------------------${RESET}"
echo ""
echo -e "After updating settings, restart VS Code or run 'Developer: Reload Window'."
