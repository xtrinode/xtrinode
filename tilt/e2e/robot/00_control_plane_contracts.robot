*** Settings ***
Documentation       Control-plane local k3d contracts: CRDs, KEDA, Redis, and readiness.
Resource            resources/local.resource
Test Tags           local    k3d    contracts    control-plane
Test Teardown       Run Keyword If Test Failed    Dump Debug

*** Test Cases ***
XTrinode And KEDA CRDs Are Installed
    Kubectl Output    get    crd    scaledobjects.keda.sh
    Kubectl Output    get    crd    xtrinodes.analytics.xtrinode.io
    Kubectl Output    get    crd    xtrinodecatalogs.analytics.xtrinode.io

Control Plane Deployments Are Available
    Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    Deployment Should Be Available    ${OPERATOR_NAMESPACE}    xtrinode-operator    1
    Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    Deployment Should Be Available    ${OPERATOR_NAMESPACE}    keda-operator    1
    Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    Deployment Should Be Available    ${OPERATOR_NAMESPACE}    keda-admission-webhooks    1
    Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    Deployment Should Be Available    ${OPERATOR_NAMESPACE}    keda-operator-metrics-apiserver    1
    Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    Deployment Should Be Available    ${API_SERVER_NAMESPACE}    xtrinode-api-server    1

Gateway And Redis Are Wired
    Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    Deployment Should Be Available    ${GATEWAY_NAMESPACE}    xtrinode-gateway    1
    Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    Deployment Should Be Available    ${GATEWAY_NAMESPACE}    xtrinode-gateway-redis    1
    ${pong}=    Kubectl Output    exec    -n    ${GATEWAY_NAMESPACE}    deployment/xtrinode-gateway-redis    --    redis-cli    ping
    Should Be Equal    ${pong}    PONG
    ${args}=    Kubectl Output    get    deployment    xtrinode-gateway    -n    ${GATEWAY_NAMESPACE}    -o    jsonpath={.spec.template.spec.containers[0].args}
    Should Contain    ${args}    --redis-enabled=true
    Should Contain    ${args}    redis://xtrinode-gateway-redis.${GATEWAY_NAMESPACE}.svc.cluster.local:6379/0

KEDA Admission Webhook Is Installed
    Kubectl Output    get    validatingwebhookconfiguration    keda-admission

XTrinode Admission Webhooks Are Installed
    ${webhooks}=    Kubectl Output    get    mutatingwebhookconfiguration,validatingwebhookconfiguration    -o    json
    ${webhooks_file}=    Set Variable    /tmp/xtrinode-admission-webhooks.json
    Create File    ${webhooks_file}    ${webhooks}
    JQ Should Match    ${webhooks_file}    any(.items[]; any(.webhooks[]?; .name == "mxtrinode.kb.io" and .clientConfig.service.name == "xtrinode-operator" and .clientConfig.service.namespace == $ns)) and any(.items[]; any(.webhooks[]?; .name == "vxtrinode.kb.io" and .clientConfig.service.name == "xtrinode-operator" and .clientConfig.service.namespace == $ns))    --arg    ns    ${OPERATOR_NAMESPACE}
