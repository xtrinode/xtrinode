*** Settings ***
Documentation       Native HPA runtime lifecycle contract: worker HPA owns scale across suspend/resume.
Resource            resources/local.resource
Suite Setup         Run Keywords    Create Local Namespace    AND    Apply Native HPA Runtime
Suite Teardown      Cleanup Native HPA Runtime
Test Teardown       Run Keyword If Test Failed    Dump Native HPA Debug
Test Tags           local    k3d    contracts    lifecycle    native-hpa    hpa

*** Variables ***
${HPA_XTRINODE_NAME}            local-trino-hpa
${HPA_CATALOG_NAME}             local-tpch-hpa
${HPA_COORDINATOR_DEPLOYMENT}   trino-${HPA_XTRINODE_NAME}-coordinator
${HPA_WORKER_DEPLOYMENT}        trino-${HPA_XTRINODE_NAME}-worker
${HPA_WORKER_HPA}               trino-${HPA_XTRINODE_NAME}-worker
${HPA_SCALEDOBJECT}             trino-${HPA_XTRINODE_NAME}-workers

*** Test Cases ***
Native HPA Suspend Resume Owns Worker Scale
    Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    Native HPA XTrinode Should Be Ready
    Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    Deployment Should Be Available    ${NAMESPACE}    ${HPA_COORDINATOR_DEPLOYMENT}    1
    Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    Deployment Should Be Available    ${NAMESPACE}    ${HPA_WORKER_DEPLOYMENT}    1
    Native HPA Should Target Worker
    Native Runtime Should Not Have KEDA ScaledObject

    Patch Native HPA XTrinode Suspended    true
    Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    Native HPA XTrinode Suspended State Should Be    true
    Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    Native HPA XTrinode Phase Should Be    Suspended
    Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    Native HPA Should Not Exist
    Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    Deployment Spec Replicas Should Equal    ${NAMESPACE}    ${HPA_COORDINATOR_DEPLOYMENT}    0
    Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    Deployment Spec Replicas Should Equal    ${NAMESPACE}    ${HPA_WORKER_DEPLOYMENT}    0

    Patch Native HPA XTrinode Suspended    false
    Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    Native HPA XTrinode Should Be Ready
    Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    Native HPA Should Target Worker
    Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    Deployment Should Be Available    ${NAMESPACE}    ${HPA_COORDINATOR_DEPLOYMENT}    1
    Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    Deployment Should Be Available    ${NAMESPACE}    ${HPA_WORKER_DEPLOYMENT}    1
    Native Runtime Should Not Have KEDA ScaledObject

*** Keywords ***
Apply Native HPA Runtime
    Cleanup Native HPA Runtime
    ${catalog}=    Catenate    SEPARATOR=
    ...    {"apiVersion":"analytics.xtrinode.io/v1","kind":"XTrinodeCatalog",
    ...    "metadata":{"name":"${HPA_CATALOG_NAME}","namespace":"${NAMESPACE}","labels":{"team":"team-local","catalog-set":"${HPA_XTRINODE_NAME}","catalog-type":"tpch"}},
    ...    "spec":{"labels":{"team":"team-local","catalog-set":"${HPA_XTRINODE_NAME}","catalog-type":"tpch"},"connector":{"tpch":{"properties":{"tpch.splits-per-node":"1"}}}}}
    ${runtime}=    Catenate    SEPARATOR=
    ...    {"apiVersion":"analytics.xtrinode.io/v1","kind":"XTrinode",
    ...    "metadata":{"name":"${HPA_XTRINODE_NAME}","namespace":"${NAMESPACE}","labels":{"team":"team-local","environment":"local"}},
    ...    "spec":{"size":"xs","minWorkers":0,"maxWorkers":1,"suspended":false,"autoSuspendAfter":"30m","wakeMinWorkers":0,"wakeTTL":"1m",
    ...    "resources":{"coordinator":{"requests":{"cpu":"250m","memory":"1Gi"},"limits":{"cpu":"1","memory":"1536Mi"}},"worker":{"requests":{"cpu":"250m","memory":"1Gi"},"limits":{"cpu":"1","memory":"1536Mi"}}},
    ...    "catalogSelector":{"matchLabels":{"team":"team-local","catalog-set":"${HPA_XTRINODE_NAME}"}},
    ...    "routing":{"header":"X-Trino-XTrinode=${HPA_XTRINODE_NAME}","routingGroup":"${HPA_XTRINODE_NAME}"},
    ...    "valuesOverlay":{"image":{"repository":"${TRINO_IMAGE_REPOSITORY}","tag":"${TRINO_IMAGE_TAG}","pullPolicy":"IfNotPresent"},
    ...    "server":{"autoscaling":{"enabled":true,"minReplicas":1,"maxReplicas":1,"targetCPUUtilizationPercentage":70,"targetMemoryUtilizationPercentage":""}},
    ...    "coordinator":{"additionalJVMConfig":["-Xmx768M","-XX:ReservedCodeCacheSize=128M"]},
    ...    "worker":{"additionalJVMConfig":["-Xmx768M","-XX:ReservedCodeCacheSize=128M"]},
    ...    "additionalConfigProperties":["query.max-memory=512MB","query.max-memory-per-node=384MB","memory.heap-headroom-per-node=256MB"]}}}
    Create File    /tmp/xtrinode-native-hpa-catalog.json    ${catalog}
    Create File    /tmp/xtrinode-native-hpa-runtime.json    ${runtime}
    Command Should Succeed    kubectl    apply    -f    /tmp/xtrinode-native-hpa-catalog.json
    Command Should Succeed    kubectl    apply    -f    /tmp/xtrinode-native-hpa-runtime.json

Native HPA XTrinode Should Be Ready
    Command Should Succeed    kubectl    wait    xtrinode/${HPA_XTRINODE_NAME}    -n    ${NAMESPACE}    --for=condition=Ready=True    --timeout=30s

Native HPA XTrinode Suspended State Should Be
    [Arguments]    ${expected}
    ${value}=    Kubectl Output    get    xtrinode    ${HPA_XTRINODE_NAME}    -n    ${NAMESPACE}    -o    jsonpath={.spec.suspended}
    ${value}=    Set Variable If    '${value}' == ''    false    ${value}
    Should Be Equal    ${value}    ${expected}

Native HPA XTrinode Phase Should Be
    [Arguments]    ${expected}
    ${phase}=    Kubectl Output    get    xtrinode    ${HPA_XTRINODE_NAME}    -n    ${NAMESPACE}    -o    jsonpath={.status.phase}
    Should Be Equal    ${phase}    ${expected}

Native HPA Should Target Worker
    ${hpa_json}=    Kubectl Output    get    hpa    ${HPA_WORKER_HPA}    -n    ${NAMESPACE}    -o    json
    Create File    /tmp/xtrinode-native-hpa.json    ${hpa_json}
    JQ Should Match    /tmp/xtrinode-native-hpa.json    .spec.scaleTargetRef.kind == "Deployment" and .spec.scaleTargetRef.name == $target and .spec.minReplicas == 1 and .spec.maxReplicas == 1    --arg    target    ${HPA_WORKER_DEPLOYMENT}

Native HPA Should Not Exist
    ${result}=    Run Command Allow Failure    kubectl    get    hpa    ${HPA_WORKER_HPA}    -n    ${NAMESPACE}
    Should Not Be Equal As Integers    ${result.rc}    0

Native Runtime Should Not Have KEDA ScaledObject
    ${result}=    Run Command Allow Failure    kubectl    get    scaledobject    ${HPA_SCALEDOBJECT}    -n    ${NAMESPACE}
    Should Not Be Equal As Integers    ${result.rc}    0

Patch Native HPA XTrinode Suspended
    [Arguments]    ${suspended}
    ${patch}=    Set Variable    {"spec":{"suspended":${suspended}}}
    Command Should Succeed    kubectl    patch    xtrinode/${HPA_XTRINODE_NAME}    -n    ${NAMESPACE}    --type=merge    -p    ${patch}

Cleanup Native HPA Runtime
    Run Command Allow Failure    kubectl    patch    xtrinode/${HPA_XTRINODE_NAME}    -n    ${NAMESPACE}    --type=merge    -p    {"metadata":{"finalizers":[]}}
    Run Command Allow Failure    kubectl    delete    xtrinode/${HPA_XTRINODE_NAME}    -n    ${NAMESPACE}    --wait=false    --ignore-not-found=true
    Run Command Allow Failure    kubectl    delete    xtrinodecatalog/${HPA_CATALOG_NAME}    -n    ${NAMESPACE}    --ignore-not-found=true
    Run Command Allow Failure    kubectl    delete    hpa/${HPA_WORKER_HPA}    -n    ${NAMESPACE}    --ignore-not-found=true
    Run Command Allow Failure    kubectl    delete    deployment/${HPA_COORDINATOR_DEPLOYMENT}    -n    ${NAMESPACE}    --ignore-not-found=true
    Run Command Allow Failure    kubectl    delete    deployment/${HPA_WORKER_DEPLOYMENT}    -n    ${NAMESPACE}    --ignore-not-found=true

Dump Native HPA Debug
    Dump Debug
    Run Command Allow Failure    kubectl    get    xtrinode    ${HPA_XTRINODE_NAME}    -n    ${NAMESPACE}    -o    yaml
    Run Command Allow Failure    kubectl    get    hpa    ${HPA_WORKER_HPA}    -n    ${NAMESPACE}    -o    yaml
    Run Command Allow Failure    kubectl    get    deployment    ${HPA_COORDINATOR_DEPLOYMENT}    -n    ${NAMESPACE}    -o    yaml
    Run Command Allow Failure    kubectl    get    deployment    ${HPA_WORKER_DEPLOYMENT}    -n    ${NAMESPACE}    -o    yaml
