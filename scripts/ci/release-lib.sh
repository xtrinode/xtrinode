#!/usr/bin/env bash

chart_field() {
  local chart="${1:?chart path is required}"
  local field="${2:?chart field is required}"

  awk -v key="${field}:" '
    $1 == key {
      value = $2
      gsub(/"/, "", value)
      print value
      exit
    }
  ' "${chart}"
}

chart_field_at_ref() {
  local ref="${1:?git ref is required}"
  local chart="${2:?chart path is required}"
  local field="${3:?chart field is required}"

  git show "${ref}:${chart}" 2>/dev/null |
    awk -v key="${field}:" '
      $1 == key {
        value = $2
        gsub(/"/, "", value)
        print value
        exit
      }
    ' || true
}

dependency_version() {
  local dependency="${1:?dependency name is required}"

  awk -v dependency="${dependency}" '
    $1 == "-" && $2 == "name:" {
      name = $3
      gsub(/"/, "", name)
      in_dependency = (name == dependency)
      next
    }
    in_dependency && $1 == "version:" {
      version = $2
      gsub(/"/, "", version)
      print version
      exit
    }
  ' helm/xtrinode/Chart.yaml
}

dependency_version_at_ref() {
  local ref="${1:?git ref is required}"
  local dependency="${2:?dependency name is required}"

  git show "${ref}:helm/xtrinode/Chart.yaml" 2>/dev/null |
    awk -v dependency="${dependency}" '
      $1 == "-" && $2 == "name:" {
        name = $3
        gsub(/"/, "", name)
        in_dependency = (name == dependency)
        next
      }
      in_dependency && $1 == "version:" {
        version = $2
        gsub(/"/, "", version)
        print version
        exit
      }
    ' || true
}

dependency_lock_version() {
  local dependency="${1:?dependency name is required}"

  awk -v dependency="${dependency}" '
    $1 == "-" && $2 == "name:" {
      name = $3
      gsub(/"/, "", name)
      in_dependency = (name == dependency)
      next
    }
    in_dependency && $1 == "version:" {
      version = $2
      gsub(/"/, "", version)
      print version
      exit
    }
  ' helm/xtrinode/Chart.lock
}

dependency_lock_version_at_ref() {
  local ref="${1:?git ref is required}"
  local dependency="${2:?dependency name is required}"

  git show "${ref}:helm/xtrinode/Chart.lock" 2>/dev/null |
    awk -v dependency="${dependency}" '
      $1 == "-" && $2 == "name:" {
        name = $3
        gsub(/"/, "", name)
        in_dependency = (name == dependency)
        next
      }
      in_dependency && $1 == "version:" {
        version = $2
        gsub(/"/, "", version)
        print version
        exit
      }
    ' || true
}

codeowners_at_ref() {
  local ref="${1:?git ref is required}"

  git show "${ref}:.github/CODEOWNERS" |
    awk '
      NF == 0 || $1 ~ /^#/ { next }
      {
        for (i = 2; i <= NF; i++) {
          if ($i ~ /^@/) print $i
        }
      }
    ' |
    sort -u
}

release_components() {
  printf '%s\n' operator gateway api-server
}

component_chart_path() {
  local component="${1:?component is required}"

  case "${component}" in
    operator) printf '%s\n' helm/xtrinode-operator/Chart.yaml ;;
    gateway) printf '%s\n' helm/xtrinode-gateway/Chart.yaml ;;
    api-server) printf '%s\n' helm/xtrinode-api-server/Chart.yaml ;;
    *)
      echo "unknown release component: ${component}" >&2
      return 1
      ;;
  esac
}

component_dependency_name() {
  local component="${1:?component is required}"

  case "${component}" in
    operator) printf '%s\n' xtrinode-operator ;;
    gateway) printf '%s\n' xtrinode-gateway ;;
    api-server) printf '%s\n' xtrinode-api-server ;;
    *)
      echo "unknown release component: ${component}" >&2
      return 1
      ;;
  esac
}

component_image_name() {
  local component="${1:?component is required}"

  case "${component}" in
    operator) printf '%s\n' xtrinode-operator ;;
    gateway) printf '%s\n' xtrinode-gateway ;;
    api-server) printf '%s\n' xtrinode-api-server ;;
    *)
      echo "unknown release component: ${component}" >&2
      return 1
      ;;
  esac
}

component_package() {
  local component="${1:?component is required}"

  case "${component}" in
    operator) printf '%s\n' ./cmd/operator ;;
    gateway) printf '%s\n' ./cmd/gateway ;;
    api-server) printf '%s\n' ./cmd/api-server ;;
    *)
      echo "unknown release component: ${component}" >&2
      return 1
      ;;
  esac
}

component_port() {
  local component="${1:?component is required}"

  case "${component}" in
    operator) printf '%s\n' 8081 ;;
    gateway) printf '%s\n' 8080 ;;
    api-server) printf '%s\n' 8081 ;;
    *)
      echo "unknown release component: ${component}" >&2
      return 1
      ;;
  esac
}

validate_semver() {
  local value="${1:-}"
  local label="${2:?label is required}"
  local prerelease_identifier release_version_regex

  prerelease_identifier='(0|[1-9][0-9]*|[0-9A-Za-z-]*[A-Za-z-][0-9A-Za-z-]*)'
  release_version_regex="^(0|[1-9][0-9]*)\\.(0|[1-9][0-9]*)\\.(0|[1-9][0-9]*)(-(${prerelease_identifier})(\\.${prerelease_identifier})*)?$"

  if ! [[ "${value}" =~ ${release_version_regex} ]]; then
    echo "::error::${label} '${value:-missing}' is not a valid release version."
    echo "::error::Expected MAJOR.MINOR.PATCH or MAJOR.MINOR.PATCH-PRERELEASE; build metadata is not supported in Docker tags."
    exit 1
  fi
}

validate_release_version_metadata() {
  local umbrella_version umbrella_app_version
  local app_version chart chart_version component dependency lock_version version

  umbrella_version="$(chart_field helm/xtrinode/Chart.yaml version)"
  umbrella_app_version="$(chart_field helm/xtrinode/Chart.yaml appVersion)"

  validate_semver "${umbrella_version}" "Umbrella chart version"
  validate_semver "${umbrella_app_version}" "Umbrella chart appVersion"

  for component in $(release_components); do
    chart="$(component_chart_path "${component}")"
    dependency="$(component_dependency_name "${component}")"
    chart_version="$(chart_field "${chart}" version)"
    app_version="$(chart_field "${chart}" appVersion)"
    version="$(dependency_version "${dependency}")"
    lock_version="$(dependency_lock_version "${dependency}")"

    validate_semver "${chart_version}" "${chart} version"
    validate_semver "${app_version}" "${chart} appVersion"

    if [ "${version}" != "${chart_version}" ]; then
      echo "::error::helm/xtrinode/Chart.yaml dependency ${dependency} version is ${version:-missing}, expected ${chart_version}."
      exit 1
    fi

    if [ "${lock_version}" != "${chart_version}" ]; then
      echo "::error::helm/xtrinode/Chart.lock dependency ${dependency} version is ${lock_version:-missing}, expected ${chart_version}."
      echo "::error::Run helm dependency update helm/xtrinode after component chart version changes."
      exit 1
    fi
  done
}

release_metadata_changed_since() {
  local ref="${1:?git ref is required}"
  local component dependency path

  if [ "$(chart_field helm/xtrinode/Chart.yaml version)" != "$(chart_field_at_ref "${ref}" helm/xtrinode/Chart.yaml version)" ]; then
    return 0
  fi

  if [ "$(chart_field helm/xtrinode/Chart.yaml appVersion)" != "$(chart_field_at_ref "${ref}" helm/xtrinode/Chart.yaml appVersion)" ]; then
    return 0
  fi

  for component in $(release_components); do
    path="$(component_chart_path "${component}")"
    dependency="$(component_dependency_name "${component}")"

    if [ "$(chart_field "${path}" version)" != "$(chart_field_at_ref "${ref}" "${path}" version)" ]; then
      return 0
    fi
    if [ "$(chart_field "${path}" appVersion)" != "$(chart_field_at_ref "${ref}" "${path}" appVersion)" ]; then
      return 0
    fi

    if [ "$(dependency_version "${dependency}")" != "$(dependency_version_at_ref "${ref}" "${dependency}")" ]; then
      return 0
    fi
    if [ "$(dependency_lock_version "${dependency}")" != "$(dependency_lock_version_at_ref "${ref}" "${dependency}")" ]; then
      return 0
    fi
  done

  return 1
}
