*** Settings ***
Documentation       Local fake-CAPI node-pool contracts for defaulting and readiness gates.
Resource            resources/local.resource
Suite Setup         Ensure NodePool Contract Prerequisites
Suite Teardown      Cleanup NodePool Contract Objects
Test Tags           local    k3d    contracts    nodepool
Test Teardown       Run Keyword If Test Failed    Dump Debug

*** Variables ***
${NODEPOOL_DEFAULT_RUNTIME}       nodepool-default-readiness
${NODEPOOL_SUSPENDED_RUNTIME}     nodepool-suspended-readiness
${FAKE_CAPI_CRDS}                 ${REPO_ROOT}/tilt/e2e/fixtures/fake-capi-nodepool-crds.yaml
${NODEPOOL_MACHINEPOOL_FILE}      /tmp/xtrinode-nodepool-default-machinepool.json
${NODEPOOL_INFRA_FILE}            /tmp/xtrinode-nodepool-default-infra.json
${NODEPOOL_SUSPENDED_FILE}        /tmp/xtrinode-nodepool-suspended.json

*** Test Cases ***
Operator Node Pool Defaults Gate Runtime Until Required Replicas Are Ready
    [Teardown]    Run Keywords    Run Keyword If Test Failed    Dump Debug    AND    Cleanup NodePool Contract Objects
    Cleanup NodePool Contract Objects
    Apply NodePool Default Runtime
    Wait Until Keyword Succeeds    180s    ${POLL_INTERVAL}    MachinePool Should Have Operator Defaults
    Wait Until Keyword Succeeds    180s    ${POLL_INTERVAL}    GCPManagedMachinePool Should Have Operator Defaults
    Wait Until Keyword Succeeds    180s    ${POLL_INTERVAL}    XTrinode NodePoolReady Should Be Provisioning    ${NODEPOOL_DEFAULT_RUNTIME}

    Sleep    25s
    Runtime Deployment Should Not Exist    ${NODEPOOL_DEFAULT_RUNTIME}    coordinator
    Runtime Deployment Should Not Exist    ${NODEPOOL_DEFAULT_RUNTIME}    worker

    Patch Fake MachinePool Status    2    2
    Wait Until Keyword Succeeds    180s    ${POLL_INTERVAL}    Runtime Deployment Should Exist    ${NODEPOOL_DEFAULT_RUNTIME}    coordinator

Suspended Keep Warm Node Pool Does Not Report Ready Before Required Replicas
    [Teardown]    Run Keywords    Run Keyword If Test Failed    Dump Debug    AND    Cleanup NodePool Contract Objects
    Cleanup NodePool Contract Objects
    Apply Suspended Keep Warm NodePool Runtime
    Wait Until Keyword Succeeds    180s    ${POLL_INTERVAL}    MachinePool Should Have Operator Defaults For Runtime    ${NODEPOOL_SUSPENDED_RUNTIME}    ${NODEPOOL_MACHINEPOOL_FILE}
    Wait Until Keyword Succeeds    180s    ${POLL_INTERVAL}    XTrinode NodePoolReady Should Be Provisioning    ${NODEPOOL_SUSPENDED_RUNTIME}

    Patch Fake MachinePool Status    2    2    ${NODEPOOL_SUSPENDED_RUNTIME}
    Command Should Succeed    kubectl    wait    xtrinode/${NODEPOOL_SUSPENDED_RUNTIME}    -n    ${NAMESPACE}    --for=condition=NodePoolReady=True    --timeout=180s
    XTrinode NodePoolReady Reason Should Be    ${NODEPOOL_SUSPENDED_RUNTIME}    NodePoolReady

*** Keywords ***
Ensure NodePool Contract Prerequisites
    Create Local Namespace
    Command Should Succeed    kubectl    apply    -f    ${FAKE_CAPI_CRDS}
    Command Should Succeed    kubectl    wait    crd/machinepools.cluster.x-k8s.io    --for=condition=Established    --timeout=60s
    Command Should Succeed    kubectl    wait    crd/gcpmanagedmachinepools.infrastructure.cluster.x-k8s.io    --for=condition=Established    --timeout=60s
    Command Should Succeed    kubectl    rollout    restart    deployment/xtrinode-operator    -n    ${OPERATOR_NAMESPACE}
    Command Should Succeed    kubectl    rollout    status    deployment/xtrinode-operator    -n    ${OPERATOR_NAMESPACE}    --timeout=180s
    Wait Until Keyword Succeeds    180s    ${POLL_INTERVAL}    Deployment Should Be Available    ${OPERATOR_NAMESPACE}    xtrinode-operator    1

Apply NodePool Default Runtime
    ${manifest}=    Set Variable    /tmp/xtrinode-nodepool-default-readiness.json
    ${json}=    Set Variable    {"apiVersion":"analytics.xtrinode.io/v1","kind":"XTrinode","metadata":{"name":"${NODEPOOL_DEFAULT_RUNTIME}","namespace":"${NAMESPACE}","labels":{"test.xtrinode.io/contract":"nodepool-default-readiness"}},"spec":{"size":"xs","minWorkers":0,"maxWorkers":0,"autoSuspendAfter":"30m","keda":{"enabled":false},"operatorNodePoolDefaults":{"defaultMinNodes":2,"defaultMaxNodes":4,"defaultOSDiskGB":64},"nodePool":{"provider":"gcp","providerMode":"managed","clusterName":"fake-cluster","kubernetesVersion":"v1.29.0","gcp":{"machineType":"e2-standard-4"}},"routing":{"header":"X-Trino-XTrinode=${NAMESPACE}/${NODEPOOL_DEFAULT_RUNTIME}","routingGroup":"${NODEPOOL_DEFAULT_RUNTIME}"},"valuesOverlay":{"image":{"repository":"${TRINO_IMAGE_REPOSITORY}","tag":"${TRINO_IMAGE_TAG}","pullPolicy":"IfNotPresent"}}}}
    Create File    ${manifest}    ${json}
    Command Should Succeed    kubectl    apply    -f    ${manifest}

Apply Suspended Keep Warm NodePool Runtime
    ${manifest}=    Set Variable    /tmp/xtrinode-nodepool-suspended-readiness.json
    ${json}=    Set Variable    {"apiVersion":"analytics.xtrinode.io/v1","kind":"XTrinode","metadata":{"name":"${NODEPOOL_SUSPENDED_RUNTIME}","namespace":"${NAMESPACE}","labels":{"test.xtrinode.io/contract":"nodepool-suspended-readiness"}},"spec":{"size":"xs","suspended":true,"minWorkers":0,"maxWorkers":0,"autoSuspendAfter":"30m","keda":{"enabled":false},"operatorNodePoolDefaults":{"defaultMinNodes":2,"defaultMaxNodes":4,"defaultOSDiskGB":64},"nodePool":{"provider":"gcp","providerMode":"managed","clusterName":"fake-cluster","kubernetesVersion":"v1.29.0","scaleDownOnSuspend":false,"gcp":{"machineType":"e2-standard-4"}},"routing":{"header":"X-Trino-XTrinode=${NAMESPACE}/${NODEPOOL_SUSPENDED_RUNTIME}","routingGroup":"${NODEPOOL_SUSPENDED_RUNTIME}"},"valuesOverlay":{"image":{"repository":"${TRINO_IMAGE_REPOSITORY}","tag":"${TRINO_IMAGE_TAG}","pullPolicy":"IfNotPresent"}}}}
    Create File    ${manifest}    ${json}
    Command Should Succeed    kubectl    apply    -f    ${manifest}

MachinePool Should Have Operator Defaults
    MachinePool Should Have Operator Defaults For Runtime    ${NODEPOOL_DEFAULT_RUNTIME}    ${NODEPOOL_MACHINEPOOL_FILE}

MachinePool Should Have Operator Defaults For Runtime
    [Arguments]    ${runtime}    ${output_file}
    ${json}=    Kubectl Output    get    machinepools    ${runtime}-pool    -n    ${NAMESPACE}    -o    json
    Create File    ${output_file}    ${json}
    JQ Should Match    ${output_file}    .spec.replicas == 2 and .metadata.annotations["cluster-autoscaler/node-group-min-size"] == "2" and .metadata.annotations["cluster-autoscaler/node-group-max-size"] == "4"

XTrinode NodePoolReady Should Be Provisioning
    [Arguments]    ${runtime}
    ${json}=    Kubectl Output    get    xtrinode    ${runtime}    -n    ${NAMESPACE}    -o    json
    Create File    ${NODEPOOL_SUSPENDED_FILE}    ${json}
    JQ Should Match    ${NODEPOOL_SUSPENDED_FILE}    (.status.conditions // [] | map(select(.type == "NodePoolReady" and .status == "True")) | length) == 0
    JQ Should Match    ${NODEPOOL_SUSPENDED_FILE}    any(.status.conditions[]?; .type == "NodePoolReady" and .status == "False" and .reason == "NodePoolProvisioning")

XTrinode NodePoolReady Reason Should Be
    [Arguments]    ${runtime}    ${reason}
    ${json}=    Kubectl Output    get    xtrinode    ${runtime}    -n    ${NAMESPACE}    -o    json
    Create File    ${NODEPOOL_SUSPENDED_FILE}    ${json}
    JQ Should Match    ${NODEPOOL_SUSPENDED_FILE}    any(.status.conditions[]?; .type == "NodePoolReady" and .status == "True" and .reason == $reason)    --arg    reason    ${reason}

GCPManagedMachinePool Should Have Operator Defaults
    ${json}=    Kubectl Output    get    gcpmanagedmachinepools    ${NODEPOOL_DEFAULT_RUNTIME}-pool    -n    ${NAMESPACE}    -o    json
    Create File    ${NODEPOOL_INFRA_FILE}    ${json}
    JQ Should Match    ${NODEPOOL_INFRA_FILE}    .spec.scaling.minCount == 2 and .spec.scaling.maxCount == 4 and .spec.diskSizeGB == 64

Patch Fake MachinePool Status
    [Arguments]    ${ready_replicas}    ${replicas}    ${runtime}=${NODEPOOL_DEFAULT_RUNTIME}
    ${patch}=    Set Variable    {"status":{"readyReplicas":${ready_replicas},"replicas":${replicas}}}
    Command Should Succeed    kubectl    patch    machinepools    ${runtime}-pool    -n    ${NAMESPACE}    --subresource=status    --type=merge    -p    ${patch}

Runtime Deployment Should Not Exist
    [Arguments]    ${runtime}    ${component}
    ${result}=    Run Command Allow Failure    kubectl    get    deployment    trino-${runtime}-${component}    -n    ${NAMESPACE}
    Should Not Be Equal As Integers    ${result.rc}    0    msg=${result.stdout}

Runtime Deployment Should Exist
    [Arguments]    ${runtime}    ${component}
    Command Should Succeed    kubectl    get    deployment    trino-${runtime}-${component}    -n    ${NAMESPACE}

Cleanup NodePool Contract Objects
    Run Command Allow Failure    kubectl    patch    xtrinode/${NODEPOOL_DEFAULT_RUNTIME}    -n    ${NAMESPACE}    --type=merge    -p    {"metadata":{"finalizers":[]}}
    Run Command Allow Failure    kubectl    patch    xtrinode/${NODEPOOL_SUSPENDED_RUNTIME}    -n    ${NAMESPACE}    --type=merge    -p    {"metadata":{"finalizers":[]}}
    Run Command Allow Failure    kubectl    delete    xtrinode/${NODEPOOL_DEFAULT_RUNTIME}    -n    ${NAMESPACE}    --ignore-not-found=true    --wait=false
    Run Command Allow Failure    kubectl    delete    xtrinode/${NODEPOOL_SUSPENDED_RUNTIME}    -n    ${NAMESPACE}    --ignore-not-found=true    --wait=false
    Run Command Allow Failure    kubectl    delete    deployment,service,configmap,poddisruptionbudget,serviceaccount,horizontalpodautoscaler,scaledobject,triggerauthentication    -n    ${NAMESPACE}    -l    app.kubernetes.io/instance=${NODEPOOL_DEFAULT_RUNTIME}    --ignore-not-found=true    --wait=false
    Run Command Allow Failure    kubectl    delete    machinepools    ${NODEPOOL_DEFAULT_RUNTIME}-pool    -n    ${NAMESPACE}    --ignore-not-found=true    --wait=false
    Run Command Allow Failure    kubectl    delete    machinepools    ${NODEPOOL_SUSPENDED_RUNTIME}-pool    -n    ${NAMESPACE}    --ignore-not-found=true    --wait=false
    Run Command Allow Failure    kubectl    delete    gcpmanagedmachinepools    ${NODEPOOL_DEFAULT_RUNTIME}-pool    -n    ${NAMESPACE}    --ignore-not-found=true    --wait=false
    Run Command Allow Failure    kubectl    delete    gcpmanagedmachinepools    ${NODEPOOL_SUSPENDED_RUNTIME}-pool    -n    ${NAMESPACE}    --ignore-not-found=true    --wait=false
