*** Settings ***
Documentation       Prometheus-backed KEDA autoscaling with a custom CRD query over worker JVM metrics.
Resource            resources/local.resource
Suite Setup         Run Keywords    Ensure Prometheus Stack Ready
Suite Teardown      Stop Prometheus Port Forward
Test Tags           local    k3d    scaleout    prometheus    keda
Test Teardown       Run Keyword If Test Failed    Dump Prometheus Autoscaler Debug

*** Variables ***
${PROMETHEUS_NAMESPACE}          monitoring
${PROMETHEUS_SERVICE}            xtrinode-observability-pro-prometheus
${PROMETHEUS_OPERATOR}           xtrinode-observability-pro-operator
${PROMETHEUS_STATEFULSET}        prometheus-xtrinode-observability-pro-prometheus
${PROMETHEUS_PORT}               19090
${PROM_XTRINODE_NAME}            local-trino-prometheus
${PROM_MANIFEST}                 ${REPO_ROOT}/tilt/examples/xtrinode-prometheus-autoscale.yaml
${PROM_CATALOG_NAME}             local-prometheus-tpch
${PROM_SCALEDOBJECT}             trino-local-trino-prometheus-workers
${PROM_WORKER_DEPLOYMENT}        trino-local-trino-prometheus-worker
${PROM_COORDINATOR_DEPLOYMENT}   trino-local-trino-prometheus-coordinator

*** Test Cases ***
Custom Prometheus Worker JVM Query Scales Workers
    Reset Prometheus Autoscaler Resources
    Apply Prometheus Autoscaler Manifest
    Command Should Succeed    kubectl    wait    xtrinode/${PROM_XTRINODE_NAME}    -n    ${NAMESPACE}    --for=condition=Ready=True    --timeout=${WAIT_TIMEOUT}
    Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    Command Should Succeed    kubectl    wait    scaledobject/${PROM_SCALEDOBJECT}    -n    ${NAMESPACE}    --for=condition=Ready=True    --timeout=30s
    Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    Deployment Should Be Available    ${NAMESPACE}    ${PROM_COORDINATOR_DEPLOYMENT}    1
    Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    Deployment Should Be Available    ${NAMESPACE}    ${PROM_WORKER_DEPLOYMENT}    1
    ${query}=    Prometheus ScaledObject Query Should Be Custom Worker Query
    Wait Until Keyword Succeeds    300s    5s    Prometheus Query Should Be Greater Than    ${query}    1
    Wait Until Keyword Succeeds    420s    5s    Deployment Spec Replicas Should Equal    ${NAMESPACE}    ${PROM_WORKER_DEPLOYMENT}    2
    Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    Deployment Should Be Available    ${NAMESPACE}    ${PROM_WORKER_DEPLOYMENT}    2

*** Keywords ***
Ensure Prometheus Stack Ready
    Command Should Succeed    kubectl    wait    deployment/${PROMETHEUS_OPERATOR}    -n    ${PROMETHEUS_NAMESPACE}    --for=condition=Available    --timeout=${WAIT_TIMEOUT}
    Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    StatefulSet Should Be Ready    ${PROMETHEUS_NAMESPACE}    ${PROMETHEUS_STATEFULSET}    1
    Start Prometheus Port Forward
    Wait Until Keyword Succeeds    60s    1s    HTTP Should Succeed    http://127.0.0.1:${PROMETHEUS_PORT}/-/ready

Start Prometheus Port Forward
    Stop Prometheus Port Forward
    Start Process    kubectl    port-forward    -n    ${PROMETHEUS_NAMESPACE}    svc/${PROMETHEUS_SERVICE}    ${PROMETHEUS_PORT}:9090    stdout=/tmp/xtrinode-robot-prometheus-port-forward.log    stderr=STDOUT    alias=xtrinode-prometheus-port-forward

Stop Prometheus Port Forward
    Run Keyword And Ignore Error    Terminate Process    xtrinode-prometheus-port-forward    kill=True

StatefulSet Should Be Ready
    [Arguments]    ${namespace}    ${statefulset}    ${minimum}
    ${ready}=    Kubectl Output    get    statefulset    ${statefulset}    -n    ${namespace}    -o    jsonpath={.status.readyReplicas}
    ${ready}=    Set Variable If    '${ready}' == ''    0    ${ready}
    Should Be True    int("""${ready}""") >= int("""${minimum}""")

Reset Prometheus Autoscaler Resources
    Create Local Namespace
    Run Command Allow Failure    kubectl    patch    xtrinode/${PROM_XTRINODE_NAME}    -n    ${NAMESPACE}    --type=merge    -p    {"metadata":{"finalizers":[]}}
    Run Command Allow Failure    kubectl    delete    xtrinode/${PROM_XTRINODE_NAME}    -n    ${NAMESPACE}    --wait=false
    Run Command Allow Failure    kubectl    wait    xtrinode/${PROM_XTRINODE_NAME}    -n    ${NAMESPACE}    --for=delete    --timeout=120s
    Run Command Allow Failure    kubectl    delete    xtrinodecatalog/${PROM_CATALOG_NAME}    -n    ${NAMESPACE}    --ignore-not-found=true
    Run Command Allow Failure    kubectl    delete    deployment,service,configmap,poddisruptionbudget,serviceaccount,horizontalpodautoscaler,scaledobject,triggerauthentication    -n    ${NAMESPACE}    -l    app.kubernetes.io/instance=${PROM_XTRINODE_NAME}    --ignore-not-found=true    --wait=false
    Run Command Allow Failure    kubectl    delete    servicemonitor    -n    ${NAMESPACE}    -l    app.kubernetes.io/instance=${PROM_XTRINODE_NAME}    --ignore-not-found=true    --wait=false
    Run Command Allow Failure    kubectl    delete    pod    -n    ${NAMESPACE}    -l    app.kubernetes.io/instance=${PROM_XTRINODE_NAME}    --ignore-not-found=true    --force    --grace-period=0

Apply Prometheus Autoscaler Manifest
    Command Should Succeed    kubectl    apply    -f    ${PROM_MANIFEST}
    ${patch}=    Set Variable    {"spec":{"valuesOverlay":{"image":{"repository":"${TRINO_IMAGE_REPOSITORY}","tag":"${TRINO_IMAGE_TAG}"}}}}
    Command Should Succeed    kubectl    patch    xtrinode/${PROM_XTRINODE_NAME}    -n    ${NAMESPACE}    --type=merge    -p    ${patch}

Prometheus ScaledObject Query Should Be Custom Worker Query
    ${query}=    Kubectl Output    get    scaledobject    ${PROM_SCALEDOBJECT}    -n    ${NAMESPACE}    -o    jsonpath={.spec.triggers[0].metadata.query}
    Should Contain    ${query}    jvm_memory_bytes_used
    Should Contain    ${query}    ${PROM_WORKER_DEPLOYMENT}
    RETURN    ${query}

Prometheus Query Should Be Greater Than
    [Arguments]    ${query}    ${threshold}
    ${body}=    Set Variable    /tmp/xtrinode-prometheus-query.json
    ${result}=    Run Process    curl    -fsS    --get    --data-urlencode    query\=${query}    http://127.0.0.1:${PROMETHEUS_PORT}/api/v1/query    -o    ${body}    stderr=STDOUT
    Log    ${result.stdout}
    Should Be Equal As Integers    ${result.rc}    0    msg=${result.stdout}
    JQ Should Match    ${body}    .status == "success" and ((([.data.result[].value[1] | tonumber] | max) // 0) > $threshold)    --argjson    threshold    ${threshold}

Dump Prometheus Autoscaler Debug
    Dump Debug
    Run Command Allow Failure    kubectl    get    pods    -n    ${PROMETHEUS_NAMESPACE}    -o    wide
    Run Command Allow Failure    kubectl    get    prometheus,servicemonitor    -A
    Run Command Allow Failure    kubectl    get    scaledobject,horizontalpodautoscaler    -n    ${NAMESPACE}    -o    yaml
    Run Command Allow Failure    kubectl    logs    -n    ${OPERATOR_NAMESPACE}    deployment/xtrinode-operator    --tail=160
