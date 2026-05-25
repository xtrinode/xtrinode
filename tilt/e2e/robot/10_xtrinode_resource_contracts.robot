*** Settings ***
Documentation       XTrinode resource contracts for local real-Trino/KEDA deployment.
Resource            resources/local.resource
Suite Setup         Ensure Local XTrinode Ready
Test Tags           local    k3d    contracts    xtrinode
Test Teardown       Run Keyword If Test Failed    Dump Debug

*** Variables ***
${TYPED_RUNTIME_NAME}           typed-runtime-shape
${TYPED_RUNTIME_NODE_LABEL}     xtrinode.io/e2e-existing-pool
${TYPED_RUNTIME_NODE_VALUE}     typed-shape

*** Test Cases ***
Rendered Trino Images Use Requested Version
    ${expected}=    Set Variable    ${TRINO_IMAGE_REPOSITORY}:${TRINO_IMAGE_TAG}
    ${coordinator}=    Kubectl Output    get    deployment    trino-${XTRINODE_NAME}-coordinator    -n    ${NAMESPACE}    -o    jsonpath={.spec.template.spec.containers[0].image}
    ${worker}=    Kubectl Output    get    deployment    trino-${XTRINODE_NAME}-worker    -n    ${NAMESPACE}    -o    jsonpath={.spec.template.spec.containers[0].image}
    Should Be Equal    ${coordinator}    ${expected}
    Should Be Equal    ${worker}    ${expected}

Catalog ConfigMap Contains TPCH Contract
    ${properties}=    Kubectl Output    get    configmap    trino-catalog-local-tpch    -n    ${NAMESPACE}    -o    ${CATALOG_PROPERTIES_OUTPUT}
    Should Contain    ${properties}    connector.name=tpch
    Should Contain    ${properties}    tpch.splits-per-node=2

ScaledObject Matches Worker Scaling Contract
    ${scaledobject}=    Set Variable    /tmp/xtrinode-scaledobject-contract.json
    Command Should Succeed    kubectl    get    scaledobject    trino-${XTRINODE_NAME}-workers    -n    ${NAMESPACE}    -o    json
    ${json}=    Kubectl Output    get    scaledobject    trino-${XTRINODE_NAME}-workers    -n    ${NAMESPACE}    -o    json
    Create File    ${scaledobject}    ${json}
    ${target}=    Set Variable    trino-${XTRINODE_NAME}-worker
    JQ Should Match    ${scaledobject}    .spec.scaleTargetRef.name == $target and .spec.minReplicaCount == 1 and .spec.maxReplicaCount == 1 and any(.spec.triggers[]; .type == "memory" and .metricType == "Utilization" and .metadata.value == "80")    --arg    target    ${target}

Observed Runtime Shape Matches Preset KEDA Contract
    ${runtime}=    Set Variable    /tmp/xtrinode-observed-runtime-shape-contract.json
    ${json}=    Kubectl Output    get    xtrinode/${XTRINODE_NAME}    -n    ${NAMESPACE}    -o    json
    Create File    ${runtime}    ${json}
    JQ Should Match    ${runtime}    .status.observedRuntimeShape.hash != null and .status.observedRuntimeShape.preset == "xs" and .status.observedRuntimeShape.autoscalingMode == "keda" and .status.observedRuntimeShape.coordinator.requests.cpu == "1" and .status.observedRuntimeShape.coordinator.requests.memory == "4Gi" and .status.observedRuntimeShape.coordinator.limits.cpu == "2" and .status.observedRuntimeShape.coordinator.limits.memory == "8Gi" and .status.observedRuntimeShape.worker.requests.cpu == "1" and .status.observedRuntimeShape.worker.requests.memory == "4Gi" and .status.observedRuntimeShape.worker.limits.cpu == "2" and .status.observedRuntimeShape.worker.limits.memory == "8Gi" and .status.observedRuntimeShape.workers.min == 1 and .status.observedRuntimeShape.workers.max == 1 and .status.observedRuntimeShape.workers.quota == 1 and .status.observedRuntimeShape.workers.capacity == 1 and .status.observedRuntimeShape.capacityUnits == 1

KEDA Admission Rejects Invalid Memory Scale To Zero
    [Teardown]    Cleanup KEDA Admission Contract Objects
    Cleanup KEDA Admission Contract Objects
    ${deployment_manifest}=    Set Variable    /tmp/xtrinode-invalid-memory-zero-target.json
    ${deployment_json}=    Set Variable    {"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"invalid-memory-zero-target","namespace":"${NAMESPACE}"},"spec":{"replicas":0,"selector":{"matchLabels":{"app":"invalid-memory-zero-target"}},"template":{"metadata":{"labels":{"app":"invalid-memory-zero-target"}},"spec":{"containers":[{"name":"pause","image":"busybox:1.36","command":["/bin/sh","-c","sleep 3600"]}]}}}}
    Create File    ${deployment_manifest}    ${deployment_json}
    Command Should Succeed    kubectl    apply    -f    ${deployment_manifest}
    ${manifest}=    Set Variable    /tmp/xtrinode-invalid-memory-zero-scaledobject.json
    ${json}=    Set Variable    {"apiVersion":"keda.sh/v1alpha1","kind":"ScaledObject","metadata":{"name":"invalid-memory-zero-contract","namespace":"${NAMESPACE}"},"spec":{"scaleTargetRef":{"name":"invalid-memory-zero-target"},"minReplicaCount":0,"triggers":[{"type":"memory","metricType":"Utilization","metadata":{"value":"80"}}]}}
    Create File    ${manifest}    ${json}
    ${result}=    Run Command Allow Failure    kubectl    apply    --validate=false    -f    ${manifest}
    Should Not Be Equal As Integers    ${result.rc}    0
    Should Contain    ${result.stdout}    minReplica is 0

XTrinode Admission Webhook Rejects Invalid Min Max
    ${webhooks}=    Kubectl Output    get    validatingwebhookconfiguration,mutatingwebhookconfiguration    -o    json
    ${webhooks_file}=    Set Variable    /tmp/xtrinode-webhooks.json
    Create File    ${webhooks_file}    ${webhooks}
    JQ Should Match    ${webhooks_file}    any(.items[]; any(.webhooks[]?; .name == "vxtrinode.kb.io")) and any(.items[]; any(.webhooks[]?; .name == "mxtrinode.kb.io"))
    ${manifest}=    Set Variable    /tmp/xtrinode-invalid-minmax.json
    ${json}=    Set Variable    {"apiVersion":"analytics.xtrinode.io/v1","kind":"XTrinode","metadata":{"name":"invalid-webhook-contract","namespace":"${NAMESPACE}"},"spec":{"size":"xs","minWorkers":3,"maxWorkers":1}}
    Create File    ${manifest}    ${json}
    ${apply}=    Run Command Allow Failure    kubectl    apply    --validate=false    -f    ${manifest}
    Should Not Be Equal As Integers    ${apply.rc}    0
    Should Contain    ${apply.stdout}    minWorkers must be less than or equal to maxWorkers

XTrinode Admission Webhook Rejects KEDA And Native HPA
    ${manifest}=    Set Variable    /tmp/xtrinode-invalid-keda-native-hpa.json
    ${json}=    Set Variable    {"apiVersion":"analytics.xtrinode.io/v1","kind":"XTrinode","metadata":{"name":"invalid-keda-native-hpa-contract","namespace":"${NAMESPACE}"},"spec":{"size":"xs","keda":{"enabled":true,"scalerType":"prometheus","scalingMetric":"query"},"valuesOverlay":{"server":{"autoscaling":{"enabled":true,"targetCPUUtilizationPercentage":70}}}}}
    Create File    ${manifest}    ${json}
    ${apply}=    Run Command Allow Failure    kubectl    apply    --validate=false    -f    ${manifest}
    Should Not Be Equal As Integers    ${apply.rc}    0
    Should Contain    ${apply.stdout}    native HPA and spec.keda cannot both manage worker replicas

XTrinodeCatalog Admission Webhook Rejects Multiple Connectors
    [Teardown]    Cleanup XTrinodeCatalog Admission Contract Objects
    Cleanup XTrinodeCatalog Admission Contract Objects
    ${webhooks}=    Kubectl Output    get    validatingwebhookconfiguration    -o    json
    ${webhooks_file}=    Set Variable    /tmp/xtrinode-catalog-webhooks.json
    Create File    ${webhooks_file}    ${webhooks}
    JQ Should Match    ${webhooks_file}    any(.items[]; any(.webhooks[]?; .name == "vxtrinodecatalog.kb.io"))
    ${manifest}=    Set Variable    /tmp/xtrinodecatalog-invalid-connectors.json
    ${json}=    Set Variable    {"apiVersion":"analytics.xtrinode.io/v1","kind":"XTrinodeCatalog","metadata":{"name":"invalid-catalog-webhook-contract","namespace":"${NAMESPACE}"},"spec":{"connector":{"tpch":{},"system":{}}}}
    Create File    ${manifest}    ${json}
    ${apply}=    Run Command Allow Failure    kubectl    apply    --validate=false    -f    ${manifest}
    Should Not Be Equal As Integers    ${apply.rc}    0
    Should Match Regexp    ${apply.stdout}    exactly one connector field must be set|at most 1 properties|must have at most 1 items?

Gateway Route Config Contains Local Backend
    ${routes}=    Kubectl Output    get    configmap    trino-gateway-routes    -n    ${GATEWAY_NAMESPACE}    -o    ${GATEWAY_ROUTES_OUTPUT}
    Should Contain    ${routes}    coordinatorURL: http://trino-${XTRINODE_NAME}.${NAMESPACE}.svc.cluster.local:8080
    Should Contain    ${routes}    header: ${XTRINODE_NAME}
    Should Contain    ${routes}    capacityUnits: 1
    Should Contain    ${routes}    state: RUNNING

Typed Runtime Shape Drives Resources Placement And Route Capacity
    [Setup]    Prepare Typed Runtime Shape Contract
    [Teardown]    Cleanup Typed Runtime Shape Contract Objects
    Command Should Succeed    kubectl    wait    xtrinode/${TYPED_RUNTIME_NAME}    -n    ${NAMESPACE}    --for=condition=Ready=True    --timeout=${WAIT_TIMEOUT}
    Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    Deployment Should Be Available    ${NAMESPACE}    trino-${TYPED_RUNTIME_NAME}-coordinator    1
    Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    Deployment Should Be Available    ${NAMESPACE}    trino-${TYPED_RUNTIME_NAME}-worker    1
    ${runtime}=    Set Variable    /tmp/xtrinode-typed-runtime-shape-contract.json
    ${json}=    Kubectl Output    get    xtrinode/${TYPED_RUNTIME_NAME}    -n    ${NAMESPACE}    -o    json
    Create File    ${runtime}    ${json}
    JQ Should Match    ${runtime}    .status.observedRuntimeShape.hash != null and .status.observedRuntimeShape.preset == "xs" and .status.observedRuntimeShape.autoscalingMode == "fixed" and .status.observedRuntimeShape.coordinator.requests.cpu == "120m" and .status.observedRuntimeShape.coordinator.requests.memory == "384Mi" and .status.observedRuntimeShape.coordinator.limits.cpu == "300m" and .status.observedRuntimeShape.coordinator.limits.memory == "768Mi" and .status.observedRuntimeShape.worker.requests.cpu == "180m" and .status.observedRuntimeShape.worker.requests.memory == "512Mi" and .status.observedRuntimeShape.worker.limits.cpu == "450m" and .status.observedRuntimeShape.worker.limits.memory == "1Gi" and .status.observedRuntimeShape.workers.fixed == 1 and .status.observedRuntimeShape.workers.quota == 1 and .status.observedRuntimeShape.capacityUnits == 7 and (.status.observedRuntimeShape.nodePool.provisioningRequested // false) == false
    ${coordinator_deployment}=    Set Variable    /tmp/xtrinode-typed-runtime-shape-coordinator.json
    ${worker_deployment}=    Set Variable    /tmp/xtrinode-typed-runtime-shape-worker.json
    ${coordinator_json}=    Kubectl Output    get    deployment    trino-${TYPED_RUNTIME_NAME}-coordinator    -n    ${NAMESPACE}    -o    json
    ${worker_json}=    Kubectl Output    get    deployment    trino-${TYPED_RUNTIME_NAME}-worker    -n    ${NAMESPACE}    -o    json
    Create File    ${coordinator_deployment}    ${coordinator_json}
    Create File    ${worker_deployment}    ${worker_json}
    JQ Should Match    ${coordinator_deployment}    .spec.template.spec.nodeSelector[$label] == $value and .spec.template.spec.containers[0].resources.requests.cpu == "120m" and .spec.template.spec.containers[0].resources.requests.memory == "384Mi"    --arg    label    ${TYPED_RUNTIME_NODE_LABEL}    --arg    value    ${TYPED_RUNTIME_NODE_VALUE}
    JQ Should Match    ${worker_deployment}    .spec.template.spec.nodeSelector[$label] == $value and .spec.template.spec.containers[0].resources.requests.cpu == "180m" and .spec.template.spec.containers[0].resources.requests.memory == "512Mi"    --arg    label    ${TYPED_RUNTIME_NODE_LABEL}    --arg    value    ${TYPED_RUNTIME_NODE_VALUE}
    ${routes}=    Kubectl Output    get    configmap    trino-gateway-routes    -n    ${GATEWAY_NAMESPACE}    -o    ${GATEWAY_ROUTES_OUTPUT}
    Should Contain    ${routes}    name: ${TYPED_RUNTIME_NAME}
    Should Contain    ${routes}    capacityUnits: 7

*** Keywords ***
Prepare Typed Runtime Shape Contract
    Cleanup Typed Runtime Shape Contract Objects
    ${node}=    Kubectl Output    get    nodes    -o    jsonpath={.items[0].metadata.name}
    Command Should Succeed    kubectl    label    node    ${node}    ${TYPED_RUNTIME_NODE_LABEL}=${TYPED_RUNTIME_NODE_VALUE}    --overwrite
    ${manifest}=    Set Variable    /tmp/xtrinode-typed-runtime-shape.json
    ${json}=    Set Variable    {"apiVersion":"analytics.xtrinode.io/v1","kind":"XTrinode","metadata":{"name":"${TYPED_RUNTIME_NAME}","namespace":"${NAMESPACE}","labels":{"test.xtrinode.io/contract":"typed-runtime-shape"}},"spec":{"size":"xs","minWorkers":1,"maxWorkers":1,"autoSuspendAfter":"30m","keda":{"enabled":false},"resources":{"coordinator":{"requests":{"cpu":"120m","memory":"384Mi"},"limits":{"cpu":"300m","memory":"768Mi"}},"worker":{"requests":{"cpu":"180m","memory":"512Mi"},"limits":{"cpu":"450m","memory":"1Gi"}}},"placement":{"nodeSelector":{"${TYPED_RUNTIME_NODE_LABEL}":"${TYPED_RUNTIME_NODE_VALUE}"}},"routing":{"header":"X-Trino-XTrinode=${NAMESPACE}/${TYPED_RUNTIME_NAME}","routingGroup":"typed-runtime-shape","capacityUnits":7},"valuesOverlay":{"image":{"repository":"${TRINO_IMAGE_REPOSITORY}","tag":"${TRINO_IMAGE_TAG}","pullPolicy":"IfNotPresent"}}}}
    Create File    ${manifest}    ${json}
    Command Should Succeed    kubectl    apply    -f    ${manifest}

Cleanup Typed Runtime Shape Contract Objects
    Run Command Allow Failure    kubectl    patch    xtrinode/${TYPED_RUNTIME_NAME}    -n    ${NAMESPACE}    --type=merge    -p    {"metadata":{"finalizers":[]}}
    Run Command Allow Failure    kubectl    delete    xtrinode/${TYPED_RUNTIME_NAME}    -n    ${NAMESPACE}    --wait=false    --ignore-not-found=true
    Run Command Allow Failure    kubectl    wait    xtrinode/${TYPED_RUNTIME_NAME}    -n    ${NAMESPACE}    --for=delete    --timeout=120s
    Run Command Allow Failure    kubectl    delete    deployment,service,configmap,poddisruptionbudget,serviceaccount,horizontalpodautoscaler,scaledobject,triggerauthentication    -n    ${NAMESPACE}    -l    app.kubernetes.io/instance=${TYPED_RUNTIME_NAME}    --ignore-not-found=true    --wait=false
    Run Command Allow Failure    kubectl    label    nodes    --all    ${TYPED_RUNTIME_NODE_LABEL}-    --overwrite

Cleanup KEDA Admission Contract Objects
    Run Command Allow Failure    kubectl    delete    scaledobject    invalid-memory-zero-contract    -n    ${NAMESPACE}    --ignore-not-found=true
    Run Command Allow Failure    kubectl    delete    deployment    invalid-memory-zero-target    -n    ${NAMESPACE}    --ignore-not-found=true

Cleanup XTrinodeCatalog Admission Contract Objects
    Run Command Allow Failure    kubectl    delete    xtrinodecatalog    invalid-catalog-webhook-contract    -n    ${NAMESPACE}    --ignore-not-found=true
