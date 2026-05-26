#!/usr/bin/env bash
# ──────────────────────────────────────────────────────────────────────────────
# Step-CA UI — install/update manager
# https://github.com/UncleFi1/step-ca-ui
#
# Modes:
#   install  — clean installation, writes .env and credentials.txt
#   update   — backup + optional git checkout + docker compose up -d --build
#   backup   — export project files, .env, PostgreSQL dump and Docker volumes
#
# Output is quiet by default. Full logs go to /var/log/step-ca-ui-install.log
# or /tmp/step-ca-ui-install-<ts>.log if /var/log is not writable.
# ──────────────────────────────────────────────────────────────────────────────
set -euo pipefail

VERBOSE=0
LANG_CHOICE=""
MODE=""
ASSUME_YES=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --verbose|-v) VERBOSE=1; shift ;;
    --yes|-y) ASSUME_YES=1; shift ;;
    --lang) LANG_CHOICE="${2:-}"; shift 2 ;;
    --lang=*) LANG_CHOICE="${1#*=}"; shift ;;
    --mode) MODE="${2:-}"; shift 2 ;;
    --mode=*) MODE="${1#*=}"; shift ;;
    --install) MODE="install"; shift ;;
    --update) MODE="update"; shift ;;
    --backup) MODE="backup"; shift ;;
    *) echo "Unknown argument: $1" >&2; exit 2 ;;
  esac
done

if [[ -t 1 ]]; then
  C_RESET=$'\033[0m'; C_BOLD=$'\033[1m'; C_DIM=$'\033[2m'
  C_RED=$'\033[31m'; C_GREEN=$'\033[32m'; C_YELLOW=$'\033[33m'
  C_BLUE=$'\033[34m'; C_CYAN=$'\033[36m'
else
  C_RESET=''; C_BOLD=''; C_DIM=''; C_RED=''; C_GREEN=''; C_YELLOW=''; C_BLUE=''; C_CYAN=''
fi

normalize_lang() {
  case "${1,,}" in
    ru|rus|russian|рус|русский) echo "ru" ;;
    en|eng|english) echo "en" ;;
    *) echo "" ;;
  esac
}

LANG_CHOICE="$(normalize_lang "$LANG_CHOICE")"
if [[ -z "$LANG_CHOICE" ]]; then
  if [[ -t 0 && "$ASSUME_YES" != "1" ]]; then
    printf "%s\n" "Select language / Выберите язык:"
    printf "  ${C_BOLD}1${C_RESET}) Русский\n"
    printf "  ${C_BOLD}2${C_RESET}) English\n"
    read -r -p "$(printf "${C_CYAN}?${C_RESET} Language [1]: ")" lang_reply
    case "${lang_reply:-1}" in
      2|en|EN) LANG_CHOICE="en" ;;
      *) LANG_CHOICE="ru" ;;
    esac
  else
    LANG_CHOICE="ru"
  fi
fi

t() {
  local key="$1"
  case "$LANG_CHOICE:$key" in
    ru:banner_title) echo "STEP-CA UI — установка и обновление" ;;
    en:banner_title) echo "STEP-CA UI — install and update" ;;
    ru:banner_sub) echo "Self-hosted PKI management для вашей сети" ;;
    en:banner_sub) echo "Self-hosted PKI management for your LAN" ;;
    ru:failed) echo "Операция завершилась ошибкой" ;;
    en:failed) echo "Operation failed" ;;
    ru:error) echo "Ошибка" ;;
    en:error) echo "Error" ;;
    ru:full_log) echo "Полный лог" ;;
    en:full_log) echo "Full log" ;;
    ru:unexpected) echo "неожиданная ошибка в строке" ;;
    en:unexpected) echo "unexpected error at line" ;;
    ru:checking_env) echo "Проверка окружения              " ;;
    en:checking_env) echo "Checking environment             " ;;
    ru:checking_docker) echo "Проверка Docker                  " ;;
    en:checking_docker) echo "Checking Docker                  " ;;
    ru:checking_project) echo "Проверка файлов проекта          " ;;
    en:checking_project) echo "Checking project files           " ;;
    ru:configuration) echo "Конфигурация" ;;
    en:configuration) echo "Configuration" ;;
    ru:mode_prompt) echo "Что сделать" ;;
    en:mode_prompt) echo "Choose action" ;;
    ru:mode_install) echo "Чистая установка" ;;
    en:mode_install) echo "Clean install" ;;
    ru:mode_update) echo "Обновление существующей установки" ;;
    en:mode_update) echo "Update existing installation" ;;
    ru:mode_backup) echo "Создать бэкап без обновления" ;;
    en:mode_backup) echo "Create backup without updating" ;;
    ru:no_sudo) echo "запустите от root или установите sudo" ;;
    en:no_sudo) echo "run as root or install sudo" ;;
    ru:docker_not_running) echo "Docker daemon не запущен" ;;
    en:docker_not_running) echo "Docker daemon not running" ;;
    ru:compose_missing) echo "Docker Compose не найден" ;;
    en:compose_missing) echo "Docker Compose not found" ;;
    ru:project_missing) echo "не найдены обязательные файлы проекта" ;;
    en:project_missing) echo "required project files not found" ;;
    ru:no_private_ip) echo "Private IPv4 адреса не найдены." ;;
    en:no_private_ip) echo "No private IPv4 addresses found." ;;
    ru:enter_ip) echo "Введите IP адрес этого сервера" ;;
    en:enter_ip) echo "Enter this server IP address" ;;
    ru:use_ip) echo "Использовать этот IP или ввести другой" ;;
    en:use_ip) echo "Use this IP or enter another" ;;
    ru:multiple_ips) echo "Найдено несколько private IP:" ;;
    en:multiple_ips) echo "Multiple private IPs detected:" ;;
    ru:pick_ip) echo "Выберите номер или введите IP вручную" ;;
    en:pick_ip) echo "Pick a number or type custom IP" ;;
    ru:invalid_choice) echo "Некорректный выбор" ;;
    en:invalid_choice) echo "Invalid choice" ;;
    ru:invalid_ip) echo "Некорректный IP" ;;
    en:invalid_ip) echo "Invalid IP" ;;
    ru:port_busy) echo "Порт 443 уже занят. Для UI будет предложен 8443." ;;
    en:port_busy) echo "Port 443 is already in use. 8443 will be suggested." ;;
    ru:https_port) echo "Внешний HTTPS-порт для Step-CA UI" ;;
    en:https_port) echo "External HTTPS port for Step-CA UI" ;;
    ru:invalid_port) echo "Некорректный HTTPS-порт" ;;
    en:invalid_port) echo "Invalid HTTPS port" ;;
    ru:provisioner) echo "Provisioner email/identifier" ;;
    en:provisioner) echo "Provisioner email/identifier" ;;
    ru:timezone) echo "Часовой пояс" ;;
    en:timezone) echo "Timezone" ;;
    ru:generating_secrets) echo "Генерация секретов               " ;;
    en:generating_secrets) echo "Generating secrets               " ;;
    ru:writing_env) echo "Запись .env + credentials.txt    " ;;
    en:writing_env) echo "Writing .env + credentials.txt   " ;;
    ru:env_exists) echo ".env уже существует — перезаписать? Старые пароли будут потеряны" ;;
    en:env_exists) echo ".env already exists — overwrite? Existing passwords will be lost" ;;
    ru:aborted_env) echo "Отменено пользователем. Существующий .env сохранён." ;;
    en:aborted_env) echo "Aborted by user. Existing .env preserved." ;;
    ru:build_start) echo "Сборка и запуск контейнеров      " ;;
    en:build_start) echo "Building and starting containers " ;;
    ru:wait_ready) echo "Ожидание готовности сервисов     " ;;
    en:wait_ready) echo "Waiting for services to be ready " ;;
    ru:running) echo "Step-CA UI запущен" ;;
    en:running) echo "Step-CA UI is up and running" ;;
    ru:not_healthy) echo "Контейнеры запущены, но пока не healthy" ;;
    en:not_healthy) echo "Containers started but are not healthy yet" ;;
    ru:change_password) echo "Смените пароль admin сразу после первого входа." ;;
    en:change_password) echo "Change the admin password right after first login." ;;
    ru:self_signed) echo "TLS-сертификат self-signed — примите предупреждение браузера или импортируйте Root CA." ;;
    en:self_signed) echo "TLS certificate is self-signed — accept the browser warning or import Root CA." ;;
    ru:update_requires_env) echo "Для обновления нужен существующий .env. Для чистой установки выберите install." ;;
    en:update_requires_env) echo "Update requires an existing .env. Choose install for a clean setup." ;;
    ru:backup_start) echo "Создание бэкапа                   " ;;
    en:backup_start) echo "Creating backup                   " ;;
    ru:backup_dir) echo "Каталог бэкапа" ;;
    en:backup_dir) echo "Backup directory" ;;
    ru:backup_done) echo "Бэкап создан" ;;
    en:backup_done) echo "Backup completed" ;;
    ru:sync_ca_pw) echo "Синхронизировать CA_PASSWORD из существующего step-ca secret" ;;
    en:sync_ca_pw) echo "Sync CA_PASSWORD from existing step-ca secret" ;;
    ru:update_git) echo "Обновить код из GitHub перед пересборкой" ;;
    en:update_git) echo "Update code from GitHub before rebuild" ;;
    ru:target_version) echo "Версия/ветка/тег для checkout" ;;
    en:target_version) echo "Version/branch/tag to checkout" ;;
    ru:dirty_git) echo "Рабочее дерево Git содержит изменения. Обновление кода может их перезаписать." ;;
    en:dirty_git) echo "Git working tree has local changes. Code update may overwrite them." ;;
    ru:continue) echo "Продолжить" ;;
    en:continue) echo "Continue" ;;
    ru:update_done) echo "Обновление завершено" ;;
    en:update_done) echo "Update completed" ;;
    ru:install_done) echo "Установка завершена" ;;
    en:install_done) echo "Installation completed" ;;
    *) echo "$key" ;;
  esac
}

LOG_FILE=/var/log/step-ca-ui-install.log
if ! touch "$LOG_FILE" 2>/dev/null; then
  LOG_FILE="/tmp/step-ca-ui-install-$(date +%s).log"
  touch "$LOG_FILE"
fi
: > "$LOG_FILE"
chmod 600 "$LOG_FILE" 2>/dev/null || true

run() {
  if [[ "$VERBOSE" == "1" ]]; then
    "$@" 2>&1 | tee -a "$LOG_FILE"
    return "${PIPESTATUS[0]}"
  fi
  "$@" >>"$LOG_FILE" 2>&1
}

_log() { printf '%s\n' "$*" >>"$LOG_FILE"; }
say_step() { printf "${C_BOLD}${C_BLUE}▸${C_RESET} %s" "$*"; _log "▸ $*"; }
say_ok()   { printf " ${C_GREEN}✓${C_RESET}\n"; _log "  OK"; }
say_fail() { printf " ${C_RED}✗${C_RESET}\n"; _log "  FAIL"; }
say_warn() { printf "\n  ${C_YELLOW}⚠${C_RESET} %s\n" "$*"; _log "WARN: $*"; }
say_info() { printf "  ${C_DIM}%s${C_RESET}\n" "$*"; _log "INFO: $*"; }

die() {
  printf "\n${C_RED}${C_BOLD}✗ %s${C_RESET}\n" "$(t failed)" >&2
  printf "  %s: %s\n" "$(t error)" "$*" >&2
  printf "  %s: ${C_BOLD}%s${C_RESET}\n" "$(t full_log)" "$LOG_FILE" >&2
  exit 1
}

trap 'die "$(t unexpected) $LINENO"' ERR

ask() {
  local prompt="$1" default="${2:-}" reply
  if [[ "$ASSUME_YES" == "1" || ! -t 0 ]]; then
    echo "$default"
    return
  fi
  if [[ -n "$default" ]]; then
    read -r -p "$(printf "${C_CYAN}?${C_RESET} %s ${C_DIM}[%s]${C_RESET}: " "$prompt" "$default")" reply
    echo "${reply:-$default}"
  else
    read -r -p "$(printf "${C_CYAN}?${C_RESET} %s: " "$prompt")" reply
    echo "$reply"
  fi
}

confirm() {
  local prompt="$1" default="${2:-N}" reply
  [[ "$ASSUME_YES" == "1" ]] && return 0
  local hint="[y/N]"; [[ "${default^^}" == "Y" ]] && hint="[Y/n]"
  read -r -p "$(printf "${C_CYAN}?${C_RESET} %s ${C_DIM}%s${C_RESET}: " "$prompt" "$hint")" reply
  reply="${reply:-$default}"
  [[ "${reply^^}" =~ ^Y(ES)?$ || "${reply^^}" =~ ^Д(А)?$ ]]
}

cat <<BANNER

   ┌─────────────────────────────────────────────────┐
   │                                                 │
   │     $(t banner_title)
   │     $(t banner_sub)
   │                                                 │
   └─────────────────────────────────────────────────┘

BANNER

_log "=== install.sh started at $(date -Is) ==="
_log "Log file: $LOG_FILE"
_log "Verbose:  $VERBOSE"
_log "Language: $LANG_CHOICE"

PROJECT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$PROJECT_DIR"

SUDO=""
PKG_MANAGER=""
COMPOSE_CMD=()

docker_cmd() {
  $SUDO docker "$@"
}

compose() {
  $SUDO "${COMPOSE_CMD[@]}" "$@"
}

check_environment() {
  say_step "$(t checking_env)"
  {
    if [[ $EUID -ne 0 ]]; then
      if command -v docker >/dev/null 2>&1; then
        SUDO=""
      elif command -v sudo >/dev/null 2>&1; then
        SUDO="sudo"
      else
        die "$(t no_sudo)"
      fi
    fi

    OS_ID=""
    if [[ -f /etc/os-release ]]; then . /etc/os-release; OS_ID="${ID:-unknown}"; fi
    case "$OS_ID" in
      ubuntu|debian) PKG_MANAGER="apt" ;;
      centos|rhel|rocky|almalinux|fedora)
        PKG_MANAGER="dnf"; command -v dnf >/dev/null 2>&1 || PKG_MANAGER="yum" ;;
      *) PKG_MANAGER="" ;;
    esac
    _log "OS: $OS_ID, pkg: ${PKG_MANAGER:-(unknown)}"
    _log "Arch: $(uname -m)"
  } >>"$LOG_FILE" 2>&1
  say_ok
}

check_docker() {
  say_step "$(t checking_docker)"
  {
    if ! command -v docker >/dev/null 2>&1; then
      _log "Docker not found — installing via get.docker.com"
      if ! command -v curl >/dev/null 2>&1; then
        if [[ "$PKG_MANAGER" == "apt" ]]; then $SUDO apt-get update -qq && $SUDO apt-get install -y -qq curl
        elif [[ -n "$PKG_MANAGER" ]]; then $SUDO "$PKG_MANAGER" install -y curl
        else die "curl is required"; fi
      fi
      curl -fsSL https://get.docker.com | $SUDO sh
      $SUDO systemctl enable --now docker
    fi

    if docker compose version >/dev/null 2>&1; then
      COMPOSE_CMD=(docker compose)
    elif command -v docker-compose >/dev/null 2>&1; then
      COMPOSE_CMD=(docker-compose)
    else
      die "$(t compose_missing)"
    fi

    docker_cmd info >/dev/null 2>&1 || die "$(t docker_not_running)"
  } >>"$LOG_FILE" 2>&1
  say_ok
}

check_project_files() {
  say_step "$(t checking_project)"
  {
    [[ -f docker-compose.yml ]] || die "$(t project_missing): docker-compose.yml"
    [[ -f .env.example ]]       || die "$(t project_missing): .env.example"
    [[ -d step-ui-go ]]         || die "$(t project_missing): step-ui-go/"
    [[ -f step-ca-bootstrap.sh ]] || die "$(t project_missing): step-ca-bootstrap.sh"
  } >>"$LOG_FILE" 2>&1
  say_ok
}

choose_mode() {
  local default_mode="install"
  [[ -f .env ]] && default_mode="update"

  MODE="$(echo "$MODE" | tr '[:upper:]' '[:lower:]')"
  if [[ "$MODE" != "install" && "$MODE" != "update" && "$MODE" != "backup" ]]; then
    if [[ "$ASSUME_YES" == "1" || ! -t 0 ]]; then
      MODE="$default_mode"
    else
      printf "\n${C_BOLD}${C_BLUE}▸${C_RESET} %s\n" "$(t mode_prompt)"
      printf "  ${C_BOLD}1${C_RESET}) %s\n" "$(t mode_install)"
      printf "  ${C_BOLD}2${C_RESET}) %s\n" "$(t mode_update)"
      printf "  ${C_BOLD}3${C_RESET}) %s\n" "$(t mode_backup)"
      local mode_reply
      mode_reply="$(ask "$(t mode_prompt)" "$([[ "$default_mode" == "update" ]] && echo 2 || echo 1)")"
      case "$mode_reply" in
        2|update|Update|обновление|Обновление) MODE="update" ;;
        3|backup|Backup|бэкап|Бэкап) MODE="backup" ;;
        *) MODE="install" ;;
      esac
    fi
  fi
  _log "Mode: $MODE"
}

detect_private_ips() {
  ip -4 -o addr show 2>/dev/null \
    | awk '{
        split($4, a, "/"); ip = a[1]; iface = $2;
        if (iface ~ /^(lo|docker|br-|veth|cni|flannel|cali|tun|tailscale)/) next;
        if (ip ~ /^10\./)                             print ip "|" iface;
        else if (ip ~ /^192\.168\./)                  print ip "|" iface;
        else if (ip ~ /^172\.(1[6-9]|2[0-9]|3[01])\./) print ip "|" iface;
      }'
}

detect_timezone() {
  if command -v timedatectl >/dev/null 2>&1; then
    timedatectl show -p Timezone --value 2>/dev/null | head -n1
    return
  fi
  if [[ -f /etc/timezone ]]; then head -n1 /etc/timezone; return; fi
  if [[ -L /etc/localtime ]]; then readlink /etc/localtime | sed 's#^.*zoneinfo/##'; return; fi
  echo "UTC"
}

port_in_use() {
  if command -v ss >/dev/null 2>&1; then
    ss -ltn "( sport = :$1 )" 2>/dev/null | tail -n +2 | grep -q .
    return
  fi
  if command -v lsof >/dev/null 2>&1; then
    lsof -iTCP:"$1" -sTCP:LISTEN >/dev/null 2>&1
    return
  fi
  return 1
}

get_env_value() {
  local key="$1"
  [[ -f .env ]] || return 0
  grep -E "^${key}=" .env | tail -n1 | cut -d= -f2- || true
}

set_env_value() {
  local key="$1" value="$2" tmp found=0
  tmp="$(mktemp)"
  if [[ -f .env ]]; then
    while IFS= read -r line || [[ -n "$line" ]]; do
      if [[ "$line" == "$key="* ]]; then
        if [[ "$found" == "0" ]]; then
          printf '%s=%s\n' "$key" "$value" >>"$tmp"
          found=1
        fi
      else
        printf '%s\n' "$line" >>"$tmp"
      fi
    done < .env
  fi
  if [[ "$found" == "0" ]]; then
    printf '%s=%s\n' "$key" "$value" >>"$tmp"
  fi
  mv "$tmp" .env
  chmod 600 .env 2>/dev/null || true
}

append_missing_env() {
  local key="$1" value="$2"
  if grep -q "^${key}=" .env 2>/dev/null; then
    _log "keep env $key"
  else
    printf '%s=%s\n' "$key" "$value" >> .env
    _log "add env $key=$value"
  fi
}

app_url() {
  local host="$1" port="$2"
  if [[ "$port" == "443" ]]; then
    echo "https://${host}"
  else
    echo "https://${host}:${port}"
  fi
}

wait_for_healthy() {
  local service="$1" timeout="${2:-120}" elapsed=0 status
  while (( elapsed < timeout )); do
    status=$(compose ps --format '{{.Service}}|{{.Status}}' 2>/dev/null \
      | awk -F'|' -v svc="$service" '$1==svc {print $2; exit}')
    [[ "$status" == *"healthy"* ]]   && return 0
    [[ "$status" == *"unhealthy"* ]] && return 1
    sleep 2; ((elapsed+=2))
  done
  return 1
}

wait_all_services() {
  local url="$1" all_healthy=true code
  say_step "$(t wait_ready)"
  for svc in postgres step-ca step-ui; do
    wait_for_healthy "$svc" 120 >>"$LOG_FILE" 2>&1 || all_healthy=false
  done

  if ! $all_healthy; then
    code=$(curl -sk -o /dev/null -w "%{http_code}" --max-time 5 "${url}/login" 2>/dev/null || echo 000)
    _log "Fallback HTTPS probe: ${url}/login -> $code"
    if [[ "$code" =~ ^(200|302|303)$ ]]; then all_healthy=true; fi
  fi

  if $all_healthy; then say_ok; else say_fail; fi
  $all_healthy
}

install_mode() {
  printf "\n${C_BOLD}${C_BLUE}▸${C_RESET} %s\n" "$(t configuration)"

  local host_ip ui_port_default ui_port provisioner tz_value url
  mapfile -t PRIVATE_IPS < <(detect_private_ips)
  _log "Detected IPs: ${PRIVATE_IPS[*]}"

  if [[ "${#PRIVATE_IPS[@]}" -eq 0 ]]; then
    say_warn "$(t no_private_ip)"
    host_ip="$(ask "$(t enter_ip)")"
  elif [[ "${#PRIVATE_IPS[@]}" -eq 1 ]]; then
    local single single_ip single_iface
    single="${PRIVATE_IPS[0]}"
    single_ip="${single%%|*}"
    single_iface="${single##*|}"
    printf "  %s: ${C_BOLD}%s${C_RESET} (%s)\n" "$(t use_ip)" "$single_ip" "$single_iface"
    host_ip="$(ask "$(t use_ip)" "$single_ip")"
  else
    printf "  ${C_BOLD}%s${C_RESET}\n" "$(t multiple_ips)"
    for i in "${!PRIVATE_IPS[@]}"; do
      local entry entry_ip entry_iface
      entry="${PRIVATE_IPS[$i]}"
      entry_ip="${entry%%|*}"
      entry_iface="${entry##*|}"
      printf "    ${C_BOLD}[%d]${C_RESET}  %-15s  ${C_DIM}(%s)${C_RESET}\n" "$((i+1))" "$entry_ip" "$entry_iface"
    done
    while true; do
      local choice entry
      choice="$(ask "$(t pick_ip)" "1")"
      if [[ "$choice" =~ ^[0-9]+$ ]] && (( choice >= 1 && choice <= ${#PRIVATE_IPS[@]} )); then
        entry="${PRIVATE_IPS[$((choice-1))]}"; host_ip="${entry%%|*}"; break
      elif [[ "$choice" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
        host_ip="$choice"; break
      else
        printf "  ${C_YELLOW}⚠${C_RESET} %s: '%s'.\n" "$(t invalid_choice)" "$choice"
      fi
    done
  fi
  [[ "$host_ip" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]] || die "$(t invalid_ip): $host_ip"

  if port_in_use 443; then
    say_warn "$(t port_busy)"
    ui_port_default="8443"
  else
    ui_port_default="443"
  fi
  ui_port="$(ask "$(t https_port)" "$ui_port_default")"
  [[ "$ui_port" =~ ^[0-9]+$ ]] || die "$(t invalid_port): $ui_port"
  url="$(app_url "$host_ip" "$ui_port")"

  provisioner="$(ask "$(t provisioner)" "admin")"
  tz_value="$(detect_timezone)"
  [[ -n "$tz_value" ]] || tz_value="UTC"
  printf "  %s: ${C_BOLD}%s${C_RESET}\n" "$(t timezone)" "$tz_value"
  _log "HOST_IP=$host_ip UI_HTTPS_PORT=$ui_port PROVISIONER=$provisioner TZ=$tz_value"

  say_step "$(t generating_secrets)"
  gen_secret()  { openssl rand -base64 48 2>/dev/null | tr -dc 'A-Za-z0-9' | head -c "${1:-32}"; }
  gen_password(){ openssl rand -base64 32 2>/dev/null | tr -dc 'A-HJ-NP-Za-km-z2-9' | head -c "${1:-12}"; }
  local ca_password secret_key postgres_password admin_password
  ca_password="$(gen_secret 24)"
  secret_key="$(gen_secret 48)"
  postgres_password="$(gen_secret 24)"
  admin_password="$(gen_password 12)"
  _log "Secrets generated (values not logged)"
  say_ok

  say_step "$(t writing_env)"
  {
    if [[ -f .env ]]; then
      if confirm "$(t env_exists)" "N" >&2; then
        cp .env ".env.backup.$(date +%s)"
      else
        die "$(t aborted_env)"
      fi
    fi

    cat > .env <<EOF
# Step-CA UI — environment configuration
# Generated by install.sh on $(date '+%Y-%m-%d %H:%M:%S %Z')
HOST_IP=${host_ip}
UI_HTTPS_PORT=${ui_port}
PROVISIONER=${provisioner}
CA_PASSWORD=${ca_password}
SECRET_KEY=${secret_key}
SESSION_SECURE=true
ENABLE_HSTS=false
POSTGRES_PASSWORD=${postgres_password}
STEPUI_ADMIN_PASSWORD=${admin_password}
TZ=${tz_value}
STEPCA_DEFAULT_TLS_CERT_DURATION=8760h
STEPCA_MAX_TLS_CERT_DURATION=87600h
EOF
    chmod 600 .env

    cat > credentials.txt <<EOF
Step-CA UI — admin credentials

URL:      ${url}
Login:    admin
Password: ${admin_password}

Change this password right after first login.
This file has chmod 600. Delete it after storing the password.
EOF
    chmod 600 credentials.txt
  } >>"$LOG_FILE" 2>&1
  say_ok

  say_step "$(t build_start)"
  {
    compose pull --quiet 2>/dev/null || true
    compose up -d --build
  } >>"$LOG_FILE" 2>&1
  say_ok

  wait_all_services "$url" || true
  final_report "$url" "install" "$admin_password"
}

backup_update() {
  local kind="${1:-update}" ts backup_dir
  ts="$(date +%Y%m%d_%H%M%S)"
  backup_dir="${BACKUP_DIR:-$PROJECT_DIR/backups/$kind-$ts}"
  mkdir -p "$backup_dir/volumes"

  say_step "$(t backup_start)"
  {
    echo "$backup_dir" > "$PROJECT_DIR/backups/LATEST" 2>/dev/null || true
    tar --exclude='./.git' --exclude='./backups' --exclude='./credentials.txt' \
      -czf "$backup_dir/project-files.tgz" -C "$PROJECT_DIR" . || true
    [[ -f .env ]] && cp .env "$backup_dir/.env"

    if docker_cmd ps --format '{{.Names}}' | grep -q '^postgres$'; then
      compose exec -T postgres pg_dump -U stepui stepui > "$backup_dir/postgres-stepui.sql" || true
    fi

    local volumes
    volumes="$(for c in postgres step-ca step-ui; do
      docker_cmd inspect "$c" --format '{{range .Mounts}}{{if eq .Type "volume"}}{{.Name}}{{"\n"}}{{end}}{{end}}' 2>/dev/null || true
    done | sort -u)"

    while IFS= read -r volume; do
      [[ -n "$volume" ]] || continue
      local mountpoint
      mountpoint="$(docker_cmd volume inspect "$volume" --format '{{.Mountpoint}}' 2>/dev/null || true)"
      [[ -n "$mountpoint" && -d "$mountpoint" ]] || continue
      tar -czf "$backup_dir/volumes/$volume.tgz" -C "$mountpoint" . || true
    done <<< "$volumes"

    {
      printf '{\n'
      printf '  "format": "step-ca-ui-cli-backup-v1",\n'
      printf '  "created_at": "%s",\n' "$(date -Is)"
      printf '  "kind": "%s",\n' "$kind"
      printf '  "project_dir": "%s",\n' "$PROJECT_DIR"
      printf '  "components": [\n'
      local first=1 file rel size sum
      while IFS= read -r file; do
        [[ -f "$file" ]] || continue
        rel="${file#$backup_dir/}"
        size="$(wc -c < "$file" | tr -d ' ')"
        sum="$(sha256sum "$file" | awk '{print $1}')"
        if [[ "$first" == "0" ]]; then printf ',\n'; fi
        first=0
        printf '    {"path": "%s", "size": %s, "sha256": "%s"}' "$rel" "$size" "$sum"
      done < <(find "$backup_dir" -type f ! -name manifest.json | sort)
      printf '\n  ]\n'
      printf '}\n'
    } > "$backup_dir/manifest.json"
  } >>"$LOG_FILE" 2>&1
  say_ok
  say_info "$(t backup_dir): $backup_dir"
}

sync_ca_password_from_existing() {
  if ! docker_cmd ps -a --format '{{.Names}}' | grep -q '^step-ca$'; then
    return
  fi
  if ! confirm "$(t sync_ca_pw)" "Y"; then
    return
  fi
  local secret
  secret="$(docker_cmd exec step-ca sh -c 'cat /home/step/secrets/password 2>/dev/null || true' | tr -d '\r\n')"
  if [[ -n "$secret" ]]; then
    set_env_value CA_PASSWORD "$secret"
    _log "CA_PASSWORD synced from existing step-ca secret (value redacted)"
  fi
}

ensure_update_env() {
	[[ -f .env ]] || die "$(t update_requires_env)"
	local host_ip ui_port tz_value provisioner
	host_ip="$(get_env_value HOST_IP)"
	[[ -n "$host_ip" ]] || host_ip="127.0.0.1"
	ui_port="$(get_env_value UI_HTTPS_PORT)"
	[[ -n "$ui_port" ]] || ui_port="443"
	tz_value="$(get_env_value TZ)"
	[[ -n "$tz_value" ]] || tz_value="$(detect_timezone)"
	[[ -n "$tz_value" ]] || tz_value="UTC"
	provisioner="$(get_env_value PROVISIONER)"
	[[ -n "$provisioner" ]] || provisioner="admin"

	append_missing_env UI_HTTPS_PORT "$ui_port"
	append_missing_env TZ "$tz_value"
	append_missing_env SESSION_SECURE true
	append_missing_env STEPCA_DEFAULT_TLS_CERT_DURATION 8760h
	append_missing_env STEPCA_MAX_TLS_CERT_DURATION 87600h
	append_missing_env PROVISIONER "$provisioner"
	append_missing_env HOST_IP "$host_ip"
}

git_update_if_requested() {
  if [[ ! -d .git ]]; then
    _log "No .git directory, skipping git update"
    return
  fi
  if ! confirm "$(t update_git)" "Y"; then
    return
  fi
  if [[ -n "$(git status --porcelain)" ]]; then
    say_warn "$(t dirty_git)"
    confirm "$(t continue)" "N" || return
  fi
  run git fetch --tags origin
  local latest target
  latest="$(git tag --list 'v*' --sort=-version:refname | head -n1)"
  [[ -n "$latest" ]] || latest="main"
  target="$(ask "$(t target_version)" "$latest")"
  run git checkout "$target"
}

update_mode() {
  [[ -f .env ]] || die "$(t update_requires_env)"
  backup_update update
  ensure_update_env
  sync_ca_password_from_existing
  git_update_if_requested

  say_step "$(t build_start)"
  {
    compose pull --quiet 2>/dev/null || true
    compose up -d --build
  } >>"$LOG_FILE" 2>&1
  say_ok

  local host_ip ui_port url
  host_ip="$(get_env_value HOST_IP)"; [[ -n "$host_ip" ]] || host_ip="127.0.0.1"
  ui_port="$(get_env_value UI_HTTPS_PORT)"; [[ -n "$ui_port" ]] || ui_port="443"
  url="$(app_url "$host_ip" "$ui_port")"
  wait_all_services "$url" || true
  final_report "$url" "update" ""
}

backup_mode() {
  [[ -f .env ]] || die "$(t update_requires_env)"
  backup_update manual
  echo
  printf "${C_GREEN}${C_BOLD}✓ %s${C_RESET}\n" "$(t backup_done)"
  echo
  printf "  ${C_DIM}%s: %s${C_RESET}\n" "$(t full_log)" "$LOG_FILE"
  _log "=== install.sh finished at $(date -Is) ==="
}

final_report() {
  local url="$1" mode="$2" password="${3:-}"
  echo
  if [[ "$mode" == "install" ]]; then
    printf "${C_GREEN}${C_BOLD}✓ %s${C_RESET}\n" "$(t install_done)"
  else
    printf "${C_GREEN}${C_BOLD}✓ %s${C_RESET}\n" "$(t update_done)"
  fi
  echo
  printf "  ${C_BOLD}URL:${C_RESET}      %s\n" "$url"
  if [[ "$mode" == "install" ]]; then
    printf "  ${C_BOLD}Login:${C_RESET}    admin\n"
    printf "  ${C_BOLD}Password:${C_RESET} %s\n" "$password"
    printf "  ${C_DIM}Credentials: credentials.txt${C_RESET}\n"
  fi
  printf "  ${C_DIM}%s: %s${C_RESET}\n" "$(t full_log)" "$LOG_FILE"
  echo
  say_warn "$(t change_password)"
  say_warn "$(t self_signed)"
  _log "=== install.sh finished at $(date -Is) ==="
}

check_environment
check_docker
check_project_files
choose_mode

case "$MODE" in
  install) install_mode ;;
  update) update_mode ;;
  backup) backup_mode ;;
  *) die "unknown mode: $MODE" ;;
esac
