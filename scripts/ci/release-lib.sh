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

release_image_version() {
  chart_field helm/xtrinode/Chart.yaml appVersion
}

validate_release_version_metadata() {
  local chart_version="${1:?release chart version is required}"
  local image_version
  local chart actual_chart_version app_version dependency version

  image_version="$(release_image_version)"

  if ! [[ "${chart_version}" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]]; then
    echo "::error::Chart version '${chart_version}' is not a valid SemVer release version."
    exit 1
  fi

  if ! [[ "${image_version}" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]]; then
    echo "::error::Image appVersion '${image_version:-missing}' is not a valid SemVer version."
    exit 1
  fi

  if [ "${image_version}" != "${chart_version}" ]; then
    echo "::error::Image appVersion '${image_version}' must match XTrinode chart version '${chart_version}'."
    exit 1
  fi

  for chart in \
    helm/xtrinode/Chart.yaml \
    helm/xtrinode-operator/Chart.yaml \
    helm/xtrinode-api-server/Chart.yaml \
    helm/xtrinode-gateway/Chart.yaml
  do
    actual_chart_version="$(chart_field "${chart}" version)"
    app_version="$(chart_field "${chart}" appVersion)"
    if [ "${actual_chart_version}" != "${chart_version}" ]; then
      echo "::error::${chart} version is ${actual_chart_version:-missing}, expected ${chart_version}."
      exit 1
    fi
    if [ "${app_version}" != "${image_version}" ]; then
      echo "::error::${chart} appVersion is ${app_version:-missing}, expected image version ${image_version}."
      exit 1
    fi
  done

  for dependency in \
    xtrinode-operator \
    xtrinode-api-server \
    xtrinode-gateway
  do
    version="$(dependency_version "${dependency}")"
    if [ "${version}" != "${chart_version}" ]; then
      echo "::error::helm/xtrinode/Chart.yaml dependency ${dependency} version is ${version:-missing}, expected ${chart_version}."
      exit 1
    fi
  done
}
