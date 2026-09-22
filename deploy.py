#!/usr/bin/env python3
"""
CNC Distributed Shell Cluster — One-Time Interactive Installer & Configurator
Supports:
  - Role 1: CNC Server + UI Dashboard
  - Role 2: CNC Worker Node
  - Role 3: Worker Tools Setup
  - Role 4: Systemd Service Manager
"""

import os
import sys
import json
import shutil
import platform
import argparse
import subprocess
from pathlib import Path

# ── ANSI Terminal Colors & Styling ─────────────────────────────────────────────
class Colors:
    RESET = "\033[0m"
    BOLD = "\033[1m"
    DIM = "\033[2m"
    CYAN = "\033[96m"
    BLUE = "\033[94m"
    GREEN = "\033[92m"
    YELLOW = "\033[93m"
    RED = "\033[91m"
    MAGENTA = "\033[95m"
    BG_BLUE = "\033[44m"

def log_info(msg):
    print(f"{Colors.BLUE}[i]{Colors.RESET} {msg}")

def log_success(msg):
    print(f"{Colors.GREEN}[✓]{Colors.RESET} {Colors.BOLD}{msg}{Colors.RESET}")

def log_warn(msg):
    print(f"{Colors.YELLOW}[!]{Colors.RESET} {msg}")

def log_error(msg):
    print(f"{Colors.RED}[✗]{Colors.RESET} {Colors.BOLD}{msg}{Colors.RESET}")

def log_step(step, total, msg):
    print(f"\n{Colors.CYAN}[{step}/{total}]{Colors.RESET} {Colors.BOLD}{msg}{Colors.RESET}")

def print_banner():
    banner = f"""{Colors.CYAN}{Colors.BOLD}
  ██████╗███╗   ██╗ ██████╗    ██████╗ ███████╗██████╗ ██╗      ██████╗ ██╗   ██╗
 ██╔════╝████╗  ██║██╔════╝    ██╔══██╗██╔════╝██╔══██╗██║     ██╔═══██╗╚██╗ ██╔╝
 ██║     ██╔██╗ ██║██║         ██║  ██║█████╗  ██████╔╝██║     ██║   ██║ ╚████╔╝ 
 ██║     ██║╚██╗██║██║         ██║  ██║██╔══╝  ██╔═══╝ ██║     ██║   ██║  ╚██╔╝  
 ╚██████╗██║ ╚████║╚██████╗    ██████╔╝███████╗██║     ███████╗╚██████╔╝   ██║   
  ╚═════╝╚═╝  ╚═══╝ ╚═════╝    ╚═════╝ ╚══════╝╚═╝     ╚══════╝ ╚═════╝    ╚═╝   
{Colors.RESET}{Colors.DIM}        CNC Distributed Cluster — Automated Interactive Setup{Colors.RESET}
    """
    print(banner)

# ── Helpers ───────────────────────────────────────────────────────────────────
def is_root():
    return os.geteuid() == 0

def run_cmd(cmd, check=True, cwd=None, capture=False):
    """Run shell command with streaming output or captured output."""
    if isinstance(cmd, list):
        cmd_str = " ".join(cmd)
    else:
        cmd_str = cmd
    
    if capture:
        res = subprocess.run(cmd_str, shell=True, cwd=cwd, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        if check and res.returncode != 0:
            raise RuntimeError(f"Command failed ({res.returncode}): {cmd_str}\n{res.stderr}")
        return res.stdout.strip()
    else:
        res = subprocess.run(cmd_str, shell=True, cwd=cwd)
        if check and res.returncode != 0:
            raise RuntimeError(f"Command failed with return code {res.returncode}: {cmd_str}")
        return res.returncode

def ask_input(prompt, default=""):
    """Interactive prompt with default value."""
    if default:
        p = f"{Colors.BOLD}{prompt}{Colors.RESET} [{Colors.CYAN}{default}{Colors.RESET}]: "
    else:
        p = f"{Colors.BOLD}{prompt}{Colors.RESET}: "
    try:
        val = input(p).strip()
        return val if val else default
    except (KeyboardInterrupt, EOFError):
        print(f"\n{Colors.YELLOW}Setup cancelled.{Colors.RESET}")
        sys.exit(0)

def ask_yes_no(prompt, default_yes=True):
    """Interactive yes/no prompt."""
    hint = "Y/n" if default_yes else "y/N"
    p = f"{Colors.BOLD}{prompt}{Colors.RESET} [{Colors.CYAN}{hint}{Colors.RESET}]: "
    try:
        val = input(p).strip().lower()
        if not val:
            return default_yes
        return val in ["y", "yes", "true", "1"]
    except (KeyboardInterrupt, EOFError):
        print(f"\n{Colors.YELLOW}Setup cancelled.{Colors.RESET}")
        sys.exit(0)

def ensure_apt_packages(packages):
    """Install system apt packages if on Debian/Ubuntu."""
    if shutil.which("apt-get"):
        log_info(f"Checking & installing system packages: {', '.join(packages)}")
        sudo = "" if is_root() else "sudo "
        run_cmd(f"{sudo}apt-get update -y")
        run_cmd(f"{sudo}apt-get install -y {' '.join(packages)}")
        log_success("System packages installed.")
    else:
        log_warn("apt-get not detected. Please ensure following are installed: " + ", ".join(packages))

def ensure_node20():
    """Ensure Node.js 20+ and npm are installed."""
    node_installed = False
    current_ver = ""
    if shutil.which("node"):
        try:
            ver = run_cmd("node -v", capture=True)
            current_ver = ver
            major = int(ver.lstrip("v").split(".")[0])
            if major >= 18:
                node_installed = True
                log_success(f"Node.js is already installed ({ver}).")
        except Exception:
            pass

    if not node_installed:
        log_info("Node.js >= 20 is required for the CNC UI Dashboard.")
        install_node = ask_yes_no("Install Node.js 20 LTS via official NodeSource repository?", default_yes=True)
        if install_node:
            sudo = "" if is_root() else "sudo "
            log_info("Setting up NodeSource Node 20 LTS repository...")
            run_cmd(f"curl -fsSL https://deb.nodesource.com/setup_20.x | {sudo}bash -")
            run_cmd(f"{sudo}apt-get install -y nodejs")
            log_success(f"Node.js installed: {run_cmd('node -v', capture=True)}")
        else:
            log_warn("Continuing without automatic Node.js installation. UI build may fail if Node is missing.")

def ensure_golang():
    """Ensure Go is installed for compiling the server and tools."""
    if shutil.which("go"):
        log_success(f"Go is already installed: {run_cmd('go version', capture=True)}")
        return True

    log_info("Go is required to compile the backend server and tools.")
    install_go = ask_yes_no("Install Go 1.23.0 automatically?", default_yes=True)
    if install_go:
        sudo = "" if is_root() else "sudo "
        log_info("Downloading and installing Go 1.23.0...")
        run_cmd("wget -q -O /tmp/go.tar.gz https://go.dev/dl/go1.23.0.linux-amd64.tar.gz")
        run_cmd(f"{sudo}rm -rf /usr/local/go && {sudo}tar -C /usr/local -xzf /tmp/go.tar.gz")
        
        # Add to path for the current session
        os.environ["PATH"] += os.pathsep + "/usr/local/go/bin"
        
        # Add to global profile for future sessions
        try:
            profile_path = "/etc/profile.d/golang.sh"
            tmp_profile = "/tmp/golang.sh"
            with open(tmp_profile, "w") as f:
                f.write('export PATH=$PATH:/usr/local/go/bin\n')
            run_cmd(f"{sudo}mv {tmp_profile} {profile_path}")
            run_cmd(f"{sudo}chmod +x {profile_path}")
        except Exception as e:
            log_warn(f"Could not add Go to global profile: {e}")
            
        log_success(f"Go installed: {run_cmd('/usr/local/go/bin/go version', capture=True)}")
        return True
    else:
        log_warn("Continuing without Go. Backend compilation will fail.")
        return False

# ── Service Management ────────────────────────────────────────────────────────
def setup_systemd_service(service_name, exec_start, working_dir, description, user=None, env_vars=None):
    """Create and enable a systemd service."""
    if not is_root() and not shutil.which("sudo"):
        log_warn("Root or sudo privileges required to configure systemd service.")
        return False

    if not user:
        user = os.environ.get("SUDO_USER") or os.environ.get("USER") or "root"

    env_lines = ""
    if env_vars:
        for k, v in env_vars.items():
            env_lines += f'Environment="{k}={v}"\n'

    service_content = f"""[Unit]
Description={description}
After=network.target

[Service]
Type=simple
User={user}
WorkingDirectory={working_dir}
{env_lines}ExecStart={exec_start}
Restart=always
RestartSec=5s
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
"""
    service_path = f"/etc/systemd/system/{service_name}.service"
    sudo = "" if is_root() else "sudo "
    
    log_info(f"Writing service file {service_path}...")
    tmp_path = f"/tmp/{service_name}.service"
    with open(tmp_path, "w") as f:
        f.write(service_content)
    
    run_cmd(f"{sudo}mv {tmp_path} {service_path}")
    run_cmd(f"{sudo}chmod 644 {service_path}")
    run_cmd(f"{sudo}systemctl daemon-reload")
    run_cmd(f"{sudo}systemctl enable {service_name}")
    log_success(f"Service {service_name} created & enabled!")
    
    start_now = ask_yes_no(f"Start {service_name} now?", default_yes=True)
    if start_now:
        run_cmd(f"{sudo}systemctl restart {service_name}")
        log_success(f"Service {service_name} is running!")
    return True

# ── Role 1: CNC Server + UI Setup ─────────────────────────────────────────────
def setup_server(install_dir=None):
    print(f"\n{Colors.BG_BLUE}{Colors.BOLD} === CNC SERVER + DASHBOARD SETUP === {Colors.RESET}\n")
    
    if not install_dir:
        default_dir = os.path.abspath(os.path.join(os.getcwd()))
        if os.path.basename(default_dir) == "bin":
            default_dir = os.path.dirname(default_dir)
        install_dir = ask_input("Target installation directory", default=default_dir)
    
    install_dir = os.path.abspath(install_dir)
    os.makedirs(install_dir, exist_ok=True)
    os.chdir(install_dir)
    log_info(f"Working in: {install_dir}")

    total_steps = 5
    # Step 1: Dependencies
    log_step(1, total_steps, "Installing System Dependencies & Firewall")
    ensure_apt_packages(["git", "curl", "wget", "jq", "build-essential", "python3"])
    ensure_node20()
    ensure_golang()

    # Configure UFW if active on Linux/Linode
    if shutil.which("ufw"):
        try:
            ufw_out = run_cmd("ufw status", capture=True)
            if "Status: active" in ufw_out:
                log_info("Configuring UFW rules for CNC ports (8080 HTTP, 9090 TCP, 3000 UI)...")
                sudo = "" if is_root() else "sudo "
                run_cmd(f"{sudo}ufw allow 8080/tcp")
                run_cmd(f"{sudo}ufw allow 9090/tcp")
                run_cmd(f"{sudo}ufw allow 3000/tcp")
                log_success("UFW firewall ports opened.")
        except Exception as e:
            log_warn(f"Could not check/configure UFW: {e}")

    # Step 2: Repositories & Binaries
    log_step(2, total_steps, "Checking Server Binary & UI Repository")
    server_bin = None
    if os.path.exists(os.path.join(install_dir, "cnc-api", "cnc-server")):
        server_bin = os.path.join(install_dir, "cnc-api", "cnc-server")
    elif os.path.exists(os.path.join(install_dir, "cnc-server-linux")):
        server_bin = os.path.join(install_dir, "cnc-server-linux")
        run_cmd(f"chmod +x {server_bin}")
    elif os.path.exists(os.path.join(install_dir, "cnc-server")):
        server_bin = os.path.join(install_dir, "cnc-server")
        run_cmd(f"chmod +x {server_bin}")
    elif not os.path.exists(os.path.join(install_dir, "cnc-api")):
        download_url = "https://github.com/rel7z/cnc-deploy/raw/refs/heads/main/cnc-server-linux"
        server_bin = os.path.join(install_dir, "cnc-server-linux")
        log_info(f"Downloading precompiled server binary from {download_url}...")
        run_cmd(f"wget -q -O {server_bin} {download_url} || curl -fsSL -o {server_bin} {download_url}")
        run_cmd(f"chmod +x {server_bin}")

    # UI source directory
    ui_dir = None
    if os.path.exists(os.path.join(install_dir, "cnc-ui", "package.json")):
        ui_dir = os.path.join(install_dir, "cnc-ui")
    elif os.path.exists(os.path.join(install_dir, "cnc", "package.json")):
        ui_dir = os.path.join(install_dir, "cnc")
    elif os.path.exists(os.path.join(install_dir, "package.json")):
        ui_dir = install_dir
    else:
        ui_target = os.path.join(install_dir, "cnc")
        log_info(f"Cloning CNC Web Dashboard from https://github.com/rel7z/cnc into {ui_target}...")
        run_cmd(f"git clone https://github.com/rel7z/cnc {ui_target}")
        ui_dir = os.path.join(ui_target, "cnc-ui")

    # Step 3: Configure server_config.json
    log_step(3, total_steps, "Configuring CNC Server (server_config.json)")
    config_path = os.path.join(install_dir, "server_config.json")
    existing_cfg = {}
    if os.path.exists(config_path):
        try:
            with open(config_path, "r") as f:
                existing_cfg = json.load(f)
        except Exception:
            pass

    http_addr = ask_input("HTTP API / Dashboard Port", default=existing_cfg.get("http_addr", ":8080"))
    tcp_addr = ask_input("TCP Worker Port", default=existing_cfg.get("tcp_addr", ":9090"))
    data_dir = ask_input("Server Data Directory", default=existing_cfg.get("data_dir", "./cnc_data"))
    max_retries = int(ask_input("Max Task Retries", default=str(existing_cfg.get("max_retries", 3))))

    # WordPress & Joomla plugins directories
    wp_plugins_dir = ask_input("WordPress Plugins Directory", default=existing_cfg.get("wp_plugins_dir", "~/plugins/wp"))
    joomla_plugins_dir = ask_input("Joomla Plugins Directory", default=existing_cfg.get("joomla_plugins_dir", "~/plugins/joomla"))

    try:
        os.makedirs(os.path.expanduser(wp_plugins_dir), exist_ok=True)
        os.makedirs(os.path.expanduser(joomla_plugins_dir), exist_ok=True)
        log_success(f"Plugin directories ready: {wp_plugins_dir}, {joomla_plugins_dir}")
    except Exception as e:
        log_warn(f"Could not create plugin directories: {e}")

    # Google Drive setup
    gdrive_exist = existing_cfg.get("gdrive", {})
    enable_gdrive = ask_yes_no("Enable Google Drive Sync & Watcher?", default_yes=gdrive_exist.get("enabled", True))
    
    gdrive_cfg = {
        "enabled": enable_gdrive,
        "watch_dir": ask_input("Watcher Directory (for auto-sync & tools output)", default=gdrive_exist.get("watch_dir", "~/merged")),
        "upload_interval": ask_input("Upload Interval", default=gdrive_exist.get("upload_interval", "1s")),
        "client_id": ask_input("Google OAuth Client ID", default=gdrive_exist.get("client_id", "720099164541-naqor10gbhs6a6uqodsnnq35ec7ol03l.apps.googleusercontent.com")),
        "client_secret": ask_input("Google OAuth Client Secret", default=gdrive_exist.get("client_secret", "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")),
        "token_path": ask_input("Token File Path", default=gdrive_exist.get("token_path", "~/merged/.gdrive_token.json")),
        "parent_folder_id": ask_input("Google Drive Parent Folder ID", default=gdrive_exist.get("parent_folder_id", "1J-NkR0WlEQ2Sqz8Z7yFXruyAlxQWjd73")),
        "state_file_path": gdrive_exist.get("state_file_path", ""),
        "delete_after_upload": ask_yes_no("Delete local file after upload?", default_yes=gdrive_exist.get("delete_after_upload", False))
    }

    server_config = {
        "http_addr": http_addr,
        "tcp_addr": tcp_addr,
        "data_dir": data_dir,
        "max_retries": max_retries,
        "heartbeat_ttl": "30s",
        "wp_plugins_dir": wp_plugins_dir,
        "joomla_plugins_dir": joomla_plugins_dir,
        "gdrive": gdrive_cfg
    }

    with open(config_path, "w") as f:
        json.dump(server_config, f, indent=2)
    
    # Also sync to cnc-api/server_config.json if directory exists
    api_cfg_path = os.path.join(install_dir, "cnc-api", "server_config.json")
    if os.path.exists(os.path.dirname(api_cfg_path)):
        with open(api_cfg_path, "w") as f:
            json.dump(server_config, f, indent=2)
            
    log_success(f"Server configuration saved to {config_path}")

    # Step 4: Build Server & UI
    log_step(4, total_steps, "Building Server & Frontend Dashboard")
    
    # Build Backend
    api_dir = None
    if os.path.exists(os.path.join(install_dir, "cnc-api", "Makefile")):
        api_dir = os.path.join(install_dir, "cnc-api")
    elif 'ui_target' in locals() and os.path.exists(os.path.join(ui_target, "cnc-api", "Makefile")):
        api_dir = os.path.join(ui_target, "cnc-api")
        
    if api_dir:
        log_info(f"Compiling server, worker, and tools via Makefile in {api_dir}...")
        run_cmd("export PATH=$PATH:/usr/local/go/bin && make build && make build-linux", cwd=api_dir)
        server_bin = os.path.join(api_dir, "cnc-server")
        if os.path.exists(server_bin):
            run_cmd(f"cp -r {api_dir}/tools {install_dir}/")
            # Ensure cnc-worker-linux is available in install_dir for remote download
            worker_linux = os.path.join(api_dir, "cnc-worker-linux")
            if os.path.exists(worker_linux):
                run_cmd(f"cp {worker_linux} {install_dir}/cnc-worker-linux")
                run_cmd(f"cp {worker_linux} {api_dir}/tools/ 2>/dev/null || true")
        log_success("Backend compiled successfully.")
    elif server_bin and os.path.exists(server_bin):
        log_success(f"Using server binary: {server_bin}")

    # Build UI
    if ui_dir and os.path.exists(ui_dir):
        log_info(f"Installing UI packages in {ui_dir} (npm install)...")
        run_cmd("npm install", cwd=ui_dir)
        log_info(f"Building UI in {ui_dir} (npm run build)...")
        run_cmd("npm run build", cwd=ui_dir)
        log_success("Frontend UI compiled successfully.")

    # Step 5: Systemd Setup
    log_step(5, total_steps, "Service Setup")
    if ask_yes_no("Create systemd services for CNC Server and CNC UI?", default_yes=True):
        if not server_bin:
            server_bin = os.path.join(install_dir, "cnc-server-linux")
        setup_systemd_service(
            "cnc-server",
            f"{server_bin} -config={config_path}",
            os.path.dirname(server_bin) if os.path.dirname(server_bin) else install_dir,
            "CNC Command and Control Server",
            env_vars={"PATH": "/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin"}
        )
        if ui_dir:
            setup_systemd_service(
                "cnc-ui",
                f"{shutil.which('npm') or 'npm'} start",
                ui_dir,
                "CNC Web Dashboard",
                env_vars={
                    "NODE_ENV": "production",
                    "PORT": "3000",
                    "HOSTNAME": "0.0.0.0",
                    "PATH": "/usr/local/bin:/usr/bin:/bin"
                }
            )

    print(f"\n{Colors.GREEN}{Colors.BOLD}===================================================={Colors.RESET}")
    print(f"{Colors.GREEN}{Colors.BOLD}  ✓ CNC Server & Dashboard Setup Complete!{Colors.RESET}")
    print(f"{Colors.GREEN}{Colors.BOLD}===================================================={Colors.RESET}")
    print(f"  • API & Workers TCP : {tcp_addr}")
    print(f"  • API HTTP Endpoint : http://localhost{http_addr}")
    print(f"  • Web Dashboard     : http://localhost:3000")
    print(f"  • Config File       : {config_path}\n")

# ── Role 2: CNC Worker Setup ──────────────────────────────────────────────────
def setup_worker(install_dir=None, server_addr=None):
    print(f"\n{Colors.BG_BLUE}{Colors.BOLD} === CNC WORKER NODE SETUP === {Colors.RESET}\n")
    
    if not install_dir:
        default_dir = os.path.abspath("./cnc-worker-node")
        install_dir = ask_input("Worker installation directory", default=default_dir)
    
    install_dir = os.path.abspath(install_dir)
    os.makedirs(install_dir, exist_ok=True)
    os.chdir(install_dir)
    log_info(f"Working in: {install_dir}")

    total_steps = 4
    # Step 1: Dependencies
    log_step(1, total_steps, "Installing System Dependencies")
    ensure_apt_packages(["git", "wget", "curl", "build-essential"])
    ensure_node20()

    sudo = "" if is_root() else "sudo "
    if shutil.which("systemctl"):
        log_info("Stopping worker service before update...")
        run_cmd(f"{sudo}systemctl stop cnc-worker || true")

    # Step 2: Binary acquisition
    log_step(2, total_steps, "Downloading / Setting Up Worker Binary")
    worker_bin = os.path.join(install_dir, "cnc-worker-linux")
    
    # Remove existing binary to prevent 'Text file busy'
    if os.path.exists(worker_bin):
        run_cmd(f"rm -f {worker_bin}")
    
    # Check if existing local binary exists in parent workspace
    local_worker = Path(__file__).resolve().parent / "cnc-api" / "cnc-worker-linux"
    if not local_worker.exists():
        local_worker = Path(__file__).resolve().parent.parent / "cnc-api" / "cnc-worker-linux"

    if local_worker.exists():
        log_info(f"Copying local binary from {local_worker}...")
        shutil.copy2(local_worker, worker_bin)
    else:
        download_url = "https://github.com/rel7z/cnc-deploy/raw/refs/heads/main/cnc-worker-linux"
        log_info(f"Downloading worker from {download_url}...")
        run_cmd(f"wget -q -O {worker_bin} {download_url}")

    run_cmd(f"chmod +x {worker_bin}")
    log_success("Worker binary ready.")

    # Step 3: Worker tools setup
    log_step(3, total_steps, "Setting Up Worker Tools (worker-tools / cms-scan)")
    tools_dir = os.path.join(install_dir, "tools")
    if not os.path.exists(tools_dir):
        if ask_yes_no("Clone worker-tools repository into ./tools?", default_yes=True):
            run_cmd(f"git clone https://github.com/rel7z/worker-tools {tools_dir}")
            run_cmd(f"chmod -R +x {tools_dir}/* 2>/dev/null || true")
            log_success("Worker tools installed into ./tools.")
    else:
        log_info("./tools directory already present. Updating tools...")
        run_cmd("git fetch origin main && git reset --hard origin/main || git pull origin main || true", cwd=tools_dir)
        run_cmd(f"chmod -R +x {tools_dir}/* 2>/dev/null || true")
        log_success("Worker tools updated.")

    # Also copy cms-scan from local repo if available
    local_cms = Path(__file__).resolve().parent / "cnc-api" / "tools" / "cms-scan"
    if not local_cms.exists():
        local_cms = Path(__file__).resolve().parent.parent / "cnc-api" / "tools" / "cms-scan"
    if local_cms.exists():
        os.makedirs(tools_dir, exist_ok=True)
        shutil.copy2(local_cms, os.path.join(tools_dir, "cms-scan"))
        run_cmd(f"chmod +x {os.path.join(tools_dir, 'cms-scan')}")
        log_success("Copied cms-scan into worker tools directory.")

    # Step 4: Configure worker_config.json
    log_step(4, total_steps, "Configuring Worker (worker_config.json)")
    config_path = os.path.join(install_dir, "worker_config.json")
    existing_cfg = {}
    if os.path.exists(config_path):
        try:
            with open(config_path, "r") as f:
                existing_cfg = json.load(f)
        except Exception:
            pass

    if not server_addr:
        server_addr = ask_input("CNC Server Address (IP:Port or Host:Port)", default=existing_cfg.get("server_addr", "localhost:9090"))
    
    default_id = existing_cfg.get("worker_id") or f"worker_{platform.node()}"
    worker_id = ask_input("Worker Identifier", default=default_id)
    
    cpu_count = os.cpu_count() or 4
    max_tasks = int(ask_input("Max Concurrent Tasks", default=str(existing_cfg.get("max_tasks", cpu_count * 2))))
    data_dir = ask_input("Worker Temporary Data Directory", default=existing_cfg.get("data_dir", "./worker_data"))

    worker_config = {
        "server_addr": server_addr,
        "worker_id": worker_id,
        "max_tasks": max_tasks,
        "data_dir": data_dir
    }

    with open(config_path, "w") as f:
        json.dump(worker_config, f, indent=2)
    log_success(f"Configuration written to {config_path}")

    # Systemd service
    if ask_yes_no("Create systemd service to auto-start this worker?", default_yes=True):
        setup_systemd_service(
            "cnc-worker",
            f"{worker_bin} -config={config_path}",
            install_dir,
            f"CNC Worker Node ({worker_id})"
        )

    print(f"\n{Colors.GREEN}{Colors.BOLD}===================================================={Colors.RESET}")
    print(f"{Colors.GREEN}{Colors.BOLD}  ✓ CNC Worker Setup Complete!{Colors.RESET}")
    print(f"{Colors.GREEN}{Colors.BOLD}===================================================={Colors.RESET}")
    print(f"  • Server Target : {server_addr}")
    print(f"  • Worker ID     : {worker_id}")
    print(f"  • Concurrency   : {max_tasks} tasks")
    print(f"  • Start Manual  : cd {install_dir} && ./cnc-worker-linux\n")

# ── Role 3: Update Existing Installation ──────────────────────────────────────
def setup_update(install_dir=None):
    print(f"\n{Colors.BG_BLUE}{Colors.BOLD} === UPDATE EXISTING CNC INSTALLATION === {Colors.RESET}\n")
    if not install_dir:
        default_dir = os.path.abspath(os.path.join(os.getcwd()))
        if os.path.basename(default_dir) == "bin":
            default_dir = os.path.dirname(default_dir)
        install_dir = ask_input("Target installation directory to update", default=default_dir)
        
    install_dir = os.path.abspath(install_dir)
    
    # Auto-adjust if they pointed to a directory that CONTAINS the worker/server folders
    if not os.path.exists(os.path.join(install_dir, "cnc-worker-linux")) and os.path.exists(os.path.join(install_dir, "cnc-worker-node", "cnc-worker-linux")):
        install_dir = os.path.join(install_dir, "cnc-worker-node")
        log_info(f"Auto-detected worker installation at {install_dir}")
        
    # Auto-detect node type based on what exists in install_dir
    ui_target = install_dir if os.path.exists(os.path.join(install_dir, "cnc-ui", "package.json")) else os.path.join(install_dir, "cnc")
    worker_target = os.path.join(install_dir, "cnc-worker-linux")
    
    is_server = os.path.exists(ui_target) and (os.path.exists(os.path.join(ui_target, "cnc-ui")) or os.path.exists(os.path.join(ui_target, "cnc-api")))
    is_worker = os.path.exists(worker_target) or os.path.basename(install_dir) == "cnc-worker-node"

    if is_server:
        log_info("Detected CNC Server installation. Updating server...")
        ensure_golang()
        ensure_node20()

        sudo = "" if is_root() else "sudo "

        log_step(1, 3, "Pulling Latest Code from GitHub")
        run_cmd("git fetch origin main", cwd=ui_target)
        try:
            run_cmd("git pull origin main", cwd=ui_target)
        except Exception as e:
            log_warn(f"Standard git pull encountered untracked or conflicting files: {e}")
            log_info("Automatically stashing local changes, clearing conflicted build artifacts, and resetting to origin/main...")
            run_cmd("git stash || true", cwd=ui_target)
            run_cmd("rm -rf cnc-api/tools cnc-api/cnc-worker-linux cnc-api/cnc-server-linux cnc-server-linux cnc-worker-linux || true", cwd=ui_target)
            run_cmd("git reset --hard origin/main", cwd=ui_target)
        log_success("Code updated.")

        log_step(2, 3, "Rebuilding Server & UI")
        api_dir = os.path.join(ui_target, "cnc-api")
        if os.path.exists(api_dir) and os.path.exists(os.path.join(api_dir, "Makefile")):
            log_info(f"Recompiling backend in {api_dir}...")
            run_cmd("export PATH=$PATH:/usr/local/go/bin && make build && make build-linux", cwd=api_dir)
            
            server_bin = os.path.join(api_dir, "cnc-server-linux")
            if not os.path.exists(server_bin):
                server_bin = os.path.join(api_dir, "cnc-server")
            if os.path.exists(server_bin):
                run_cmd(f"rm -f {install_dir}/cnc-server-linux && cp {server_bin} {install_dir}/cnc-server-linux && chmod +x {install_dir}/cnc-server-linux")
                run_cmd(f"cp {server_bin} {install_dir}/cnc-server 2>/dev/null || true")
                # Ensure the built tools are copied to the main install directory where the server runs
                run_cmd(f"cp -r {api_dir}/tools {install_dir}/")
                worker_linux = os.path.join(api_dir, "cnc-worker-linux")
                if not os.path.exists(worker_linux):
                    worker_linux = os.path.join(api_dir, "cnc-worker")
                if os.path.exists(worker_linux):
                    run_cmd(f"cp {worker_linux} {install_dir}/cnc-worker-linux && chmod +x {install_dir}/cnc-worker-linux")
                    run_cmd(f"cp {worker_linux} {api_dir}/tools/ 2>/dev/null || true")
                log_success("Backend recompiled, binaries and tools updated.")
        else:
            log_warn("Could not find cnc-api directory or Makefile. Skipping backend compilation.")

        ui_dir = os.path.join(ui_target, "cnc-ui")
        if os.path.exists(ui_dir) and os.path.exists(os.path.join(ui_dir, "package.json")):
            log_info(f"Rebuilding UI in {ui_dir}...")
            run_cmd("npm install", cwd=ui_dir)
            run_cmd("npm run build", cwd=ui_dir)
            log_success("Frontend UI rebuilt successfully.")
        else:
            log_warn("Could not find cnc-ui directory or package.json. Skipping frontend compilation.")

        log_step(3, 3, "Restarting Services")
        if shutil.which("systemctl"):
            run_cmd(f"{sudo}systemctl restart cnc-ui || true")
            log_success("Frontend UI service restarted.")
            # Restart cnc-server after a small delay so output stream finishes cleanly
            run_cmd(f"nohup bash -c 'sleep 2 && {sudo}systemctl restart cnc-server' >/dev/null 2>&1 &")
            log_success("CNC Server restart scheduled in 2 seconds.")
        else:
            log_warn("systemctl not found. Please restart your processes manually.")
            
    elif is_worker:
        log_info("Detected CNC Worker installation. Updating worker...")
        ensure_node20()
        
        sudo = "" if is_root() else "sudo "
        if shutil.which("systemctl"):
            log_info("Stopping worker service before update...")
            run_cmd(f"{sudo}systemctl stop cnc-worker || true")
            
        log_step(1, 3, "Updating Worker Binary")
        worker_bin = os.path.join(install_dir, "cnc-worker-linux")
        if os.path.exists(worker_bin):
            run_cmd(f"rm -f {worker_bin}")
        
        download_url = "https://github.com/rel7z/cnc-deploy/raw/refs/heads/main/cnc-worker-linux"
        log_info(f"Downloading latest worker binary from {download_url}...")
        run_cmd(f"wget -q -O {worker_bin} {download_url}")
        run_cmd(f"chmod +x {worker_bin}")
        log_success("Worker binary updated.")
        
        log_step(2, 3, "Updating Worker Tools")
        tools_dir = os.path.join(install_dir, "tools")
        if os.path.exists(tools_dir):
            log_info("Pulling latest tools from GitHub...")
            run_cmd("git fetch origin main && git reset --hard origin/main || git pull origin main || true", cwd=tools_dir)
            run_cmd(f"chmod -R +x {tools_dir}/* 2>/dev/null || true")
            log_success("Worker tools updated.")
        else:
            log_warn(f"No tools directory found at {tools_dir}. You might need to run Option 3.")
            
        log_step(3, 3, "Restarting Worker Service")
        if shutil.which("systemctl"):
            run_cmd(f"{sudo}systemctl restart cnc-worker || true")
            log_success("Worker service restarted.")
        else:
            log_warn("systemctl not found. Please restart cnc-worker manually.")
            
    else:
        log_error(f"Could not identify a CNC Server or Worker installation in {install_dir}.")
        return

    print(f"\n{Colors.GREEN}{Colors.BOLD}===================================================={Colors.RESET}")
    print(f"{Colors.GREEN}{Colors.BOLD}  ✓ CNC Update Complete!{Colors.RESET}")
    print(f"{Colors.GREEN}{Colors.BOLD}===================================================={Colors.RESET}\n")

# ── Main Menu ─────────────────────────────────────────────────────────────────
def interactive_menu():
    print_banner()
    print(f"{Colors.BOLD}Select installation role or action:{Colors.RESET}")
    print(f"  {Colors.CYAN}1){Colors.RESET} {Colors.BOLD}CNC Server + Dashboard{Colors.RESET}  (Full Control Node: API, UI & Watcher)")
    print(f"  {Colors.CYAN}2){Colors.RESET} {Colors.BOLD}CNC Worker Node{Colors.RESET}         (Distributed execution agent + Tools)")
    print(f"  {Colors.CYAN}3){Colors.RESET} {Colors.BOLD}Install Worker Tools{Colors.RESET}    (Clone and make worker-tools executable)")
    print(f"  {Colors.CYAN}4){Colors.RESET} {Colors.BOLD}Service Status & Info{Colors.RESET}   (Check systemd status of CNC components)")
    print(f"  {Colors.CYAN}5){Colors.RESET} {Colors.BOLD}Update Existing Node{Colors.RESET}    (Pull latest code & recompile)")
    print(f"  {Colors.CYAN}6){Colors.RESET} {Colors.BOLD}Exit{Colors.RESET}\n")

    choice = ask_input("Select an option [1-6]", default="1")
    if choice == "1":
        setup_server()
    elif choice == "2":
        setup_worker()
    elif choice == "3":
        tools_dir = ask_input("Target tools directory", default="./tools")
        run_cmd(f"git clone https://github.com/rel7z/worker-tools {tools_dir}")
        run_cmd(f"chmod -R +x {tools_dir}/* 2>/dev/null || true")
        log_success(f"Tools successfully installed into {tools_dir}")
    elif choice == "4":
        print("\nChecking systemd services:")
        for svc in ["cnc-server", "cnc-ui", "cnc-worker"]:
            print(f"\n--- {svc} ---")
            run_cmd(f"systemctl status {svc} --no-pager || true")
    elif choice == "5":
        setup_update()
    else:
        print("Exiting.")
        sys.exit(0)

def main():
    parser = argparse.ArgumentParser(description="CNC Automated Installer & Setup Tool")
    parser.add_argument("--role", choices=["server", "worker", "update"], help="Specify role to install directly")
    parser.add_argument("--dir", help="Target installation directory")
    parser.add_argument("--server-addr", help="Server address for worker setup (host:port)")
    args = parser.parse_args()

    if args.role == "server":
        print_banner()
        setup_server(args.dir)
    elif args.role == "worker":
        print_banner()
        setup_worker(args.dir, args.server_addr)
    elif args.role == "update":
        print_banner()
        setup_update(args.dir)
    else:
        interactive_menu()

if __name__ == "__main__":
    main()
