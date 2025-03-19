#!/bin/bash

set -e  # Exit if any command fails

# Determine Linux type
if [ -f /etc/redhat-release ]; then
    OS_TYPE="redhat"
elif [ -f /etc/debian_version ]; then
    OS_TYPE="debian"
else
    echo "Unsupported OS type."
    exit 1
fi
echo "Detect linux flavor is $OS_TYPE"

# Define variables
INSTALL_DIR="/usr/local/bin"
BINARY_NAME="opkssh"
GITHUB_REPO="openpubkey/opkssh"

# Define the default OpenID Providers
PROVIDER_GOOGLE="https://accounts.google.com 206584157355-7cbe4s640tvm7naoludob4ut1emii7sf.apps.googleusercontent.com 24h"
PROVIDER_MICROSOFT="https://login.microsoftonline.com/9188040d-6c67-4c5b-b112-36a304b66dad/v2.0 096ce0a3-5e72-4da8-9c86-12924b294a01 24h"
PROVIDER_GITLAB="https://gitlab.com 8d8b7024572c7fd501f64374dec6bba37096783dfcd792b3988104be08cb6923 24h"

# AuthorizedKeysCommand user
AUTH_CMD_USER="opksshuser"
AUTH_CMD_GROUP="opksshuser"
SUDOERS_PATH="/etc/sudoers.d/opkssh"

if [ "$EUID" -ne 0 ]; then
    echo "Error: This script must be run as root."
    echo "sudo $0"
    exit 1
fi


RESTART_SSH=true
LOCAL_INSTALL_FILE=""
INSTALL_VERSION="latest"
for arg in "$@"; do
    if [ "$arg" == "--no-sshd-restart" ]; then
        RESTART_SSH=false
    elif [[ "$arg" == --install-from=* ]]; then
        LOCAL_INSTALL_FILE="${arg#*=}"
    elif [[ "$arg" == --install-version=* ]]; then
        INSTALL_VERSION="${arg#*=}"
    fi
done

# Display help message
if [[ "$1" == "--help" ]]; then
    echo "Usage: $0 [OPTIONS]"
    echo ""
    echo "Options:"
    echo "  --no-sshd-restart       Do not restart SSH after installation"
    echo "  --install-from=FILEPATH Install using a local file"
    echo "  --install-version=VER   Install a specific version from GitHub"
    echo "  --help                  Display this help message"
    exit 0
fi

# Ensure wget is installed
if ! command -v wget &> /dev/null; then
    echo "Error: wget is not installed. Please install it first."
    if [ "$OS_TYPE" == "debian" ]; then
        echo "sudo apt install wget"
    elif [ "$OS_TYPE" == "redhat" ]; then
            echo "sudo yum install wget"
    else
        echo "Unsupported OS type."
    fi
    exit 1
fi

if ! command -v jq &> /dev/null; then
    echo "Error: jq is required but not installed. Please install it first."
    if [ "$OS_TYPE" == "debian" ]; then
        echo "sudo apt install jq"
    elif [ "$OS_TYPE" == "redhat" ]; then
            echo "sudo yum install jq"
    else
        echo "Unsupported OS type."
    fi
    exit 1
fi

# Checks if the group and user used by the AuthorizedKeysCommand exists if not creates it
if ! getent group "$AUTH_CMD_GROUP" >/dev/null; then
    groupadd --system "$AUTH_CMD_GROUP"
    echo "Created group: $AUTH_CMD_GROUP"
fi

# If the AuthorizedKeysCommand user does not exist, create it and add it to the group
if ! getent passwd "$AUTH_CMD_USER" >/dev/null; then
    useradd -r -M -s /sbin/nologin -g "$AUTH_CMD_GROUP" "$AUTH_CMD_USER"
    echo "Created user: $AUTH_CMD_USER with group: $AUTH_CMD_GROUP"
else
    # If the AuthorizedKeysCommand user exist, ensure it is added to the group
    usermod -aG "$AUTH_CMD_GROUP" "$AUTH_CMD_USER"
    echo "Added $AUTH_CMD_USER to group: $AUTH_CMD_GROUP"
fi

echo "--install-from option supplied, installing from local file: $LOCAL_INSTALL_FILE"
# Check if we should install from a local file
if [ -n "$LOCAL_INSTALL_FILE" ]; then
    BINARY_PATH=$LOCAL_INSTALL_FILE
    if [ ! -f "$BINARY_PATH" ]; then
        echo "Error: Specified binary path does not exist."
        exit 1
    fi
    echo "Using binary from specified path: $BINARY_PATH"
else
    if [ "$INSTALL_VERSION" == "latest" ]; then
        BINARY_URL="https://github.com/$GITHUB_REPO/releases/latest/download/opkssh-linux-amd64"
    else
        BINARY_URL="https://github.com/$GITHUB_REPO/releases/download/$INSTALL_VERSION/opkssh-linux-amd64"
    fi

    # Download the binary
    echo "Downloading version $INSTALL_VERSION of $BINARY_NAME from $BINARY_URL..."
    wget -q --show-progress -O "$BINARY_NAME" "$BINARY_URL"

    BINARY_PATH="$BINARY_NAME"
fi

# Move to installation directory
mv "$BINARY_PATH" "$INSTALL_DIR/$BINARY_NAME"

# Make the binary executable, correct permissions/ownership
chmod +x "$INSTALL_DIR/$BINARY_NAME"
chown root:${AUTH_CMD_GROUP} "$INSTALL_DIR/$BINARY_NAME"
chmod 755 "$INSTALL_DIR/$BINARY_NAME"

# Checks if SELinux is enabled and if so, ensures the context is set correctly
if command -v getenforce >/dev/null 2>&1; then
    if [ "$(getenforce)" != "Disabled" ]; then
        echo "SELinux detected. Configuring SELinux for opkssh"
        echo "  Restoring context for $INSTALL_DIR/$BINARY_NAME..."
        restorecon -v "$INSTALL_DIR/$BINARY_NAME"

        # Create temporary files for the compiled module and package
        TE_TMP="/tmp/opkssh.te"
        MOD_TMP="/tmp/opkssh.mod" # SELinux requires that modules have the same file name as the module name
        PP_TMP="/tmp/opkssh.pp"

        # Pipe the TE directives into checkmodule via /dev/stdin
        # This module grants the ability to:
        # 1. open files labeled in SELinux as var_log_t, i.e. log files.
        #  This is needed for opkssh to be log errors to its log file.
        # 2. Make TCP connections to ports labeled http_port_t. This is
        #  needed so opkssh can download the public keys of the OpenID
        #  providers.
        cat << 'EOF' > "$TE_TMP"
module opkssh 1.0;

require {
        type sudo_exec_t;
        type http_port_t;
        type sshd_t;
        type ssh_exec_t;
        type var_log_t;
        class file { execute execute_no_trans open read };
        class tcp_socket name_connect;
}

#============= sshd_t ==============

#!!!! This avc is allowed in the current policy
allow sshd_t http_port_t:tcp_socket name_connect;

#!!!! This avc is allowed in the current policy
allow sshd_t ssh_exec_t:file { execute open read };
allow sshd_t ssh_exec_t:file execute_no_trans;

#!!!! This avc is allowed in the current policy
allow sshd_t sudo_exec_t:file { execute open read };
allow sshd_t sudo_exec_t:file execute_no_trans;

#!!!! This avc is allowed in the current policy
allow sshd_t var_log_t:file open;
EOF

        echo "  Compiling SELinux module..."
        checkmodule -M -m -o "$MOD_TMP" "$TE_TMP"

        echo "  Packaging module..."
        semodule_package -o "$PP_TMP" -m "$MOD_TMP"

        echo "  Installing module..."
        semodule -i "$PP_TMP"

        rm -f "$TE_TMP" "$MOD_TMP" "$PP_TMP"
        echo "SELinux module installed successfully!"
    fi
fi

echo "Installed $BINARY_NAME to $INSTALL_DIR/$BINARY_NAME"

# Verify installation
if command -v $BINARY_NAME &> /dev/null; then
    # Setup configuration
    echo "Configuring opkssh:"
    mkdir -p /etc/opk
    touch /etc/opk/auth_id
    chown root:${AUTH_CMD_GROUP} /etc/opk/auth_id
    chmod 640 /etc/opk/auth_id

    touch /etc/opk/providers
    chown root:${AUTH_CMD_GROUP} /etc/opk/providers
    chmod 640 /etc/opk/providers

    if [ -s /etc/opk/providers ]; then
        echo "  The providers policy file (/etc/opk/providers) is not empty. Keeping existing values"
    else
        echo "$PROVIDER_GOOGLE" >> /etc/opk/providers
        echo "$PROVIDER_MICROSOFT" >> /etc/opk/providers
        echo "$PROVIDER_GITLAB" >> /etc/opk/providers
    fi

    sed -i '/^AuthorizedKeysCommand /s/^/#/' /etc/ssh/sshd_config
    sed -i '/^AuthorizedKeysCommandUser /s/^/#/' /etc/ssh/sshd_config
    echo "AuthorizedKeysCommand /usr/local/bin/opkssh verify %u %k %t" >> /etc/ssh/sshd_config
    echo "AuthorizedKeysCommandUser ${AUTH_CMD_USER}" >> /etc/ssh/sshd_config

    if [ "$RESTART_SSH" = true ]; then
        if [ "$OS_TYPE" == "debian" ]; then
            systemctl restart ssh
        elif [ "$OS_TYPE" == "redhat" ]; then
            systemctl restart sshd
        else
            echo "  Unsupported OS type."
            exit 1
        fi
    else
        echo "  --no-sshd-restart option supplied, skipping SSH restart."
    fi

    if [ ! -f "$SUDOERS_PATH" ]; then
        echo "  Creating sudoers file at $SUDOERS_PATH..."
        touch "$SUDOERS_PATH"
        chmod 440 "$SUDOERS_PATH"
    fi
    SUDOERS_RULE_READ_HOME="$AUTH_CMD_USER ALL=(ALL) NOPASSWD: /usr/local/bin/opkssh readhome *"
    if ! grep -qxF "$SUDOERS_RULE_READ_HOME" "$SUDOERS_PATH"; then
        echo "  Adding sudoers rule for $AUTH_CMD_USER..."
        echo "# This allows opkssh to call opkssh readhome <username> to read the user's policy file in /home/<username>/auth_id" >> "$SUDOERS_PATH"
        echo "$SUDOERS_RULE_READ_HOME" >> "$SUDOERS_PATH"
    fi

    touch /var/log/opkssh.log
    chown root:${AUTH_CMD_GROUP} /var/log/opkssh.log
    chmod 660 /var/log/opkssh.log

    echo "Installation successful! Run '$BINARY_NAME' to use it."
else
    echo "Installation failed."
    exit 1
fi
