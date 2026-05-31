*** Settings ***
Documentation       Local fake-CAPI node-pool contracts for defaulting and readiness gates.
Resource            resources/local.resource
Suite Setup         Ensure NodePool Contract Prerequisites
Suite Teardown      Cleanup NodePool Contract Objects
Test Tags           local    k3d    contracts    nodepool
Test Teardown       Run Keyword If Test Failed    Dump Debug

*** Variables ***
${NODEPOOL_DEFAULT_RUNTIME}       nodepool-default-readiness
${FAKE_CAPI_CRDS}                 ${REPO_ROOT}/tilt/e2e/fixtures/fake-capi-nodepool-crds.yaml
${NODEPOOL_MACHINEPOOL_FILE}      /tmp/xtrinode-nodepool-default-machinepool.json
${NODEPOOL_INFRA_FILE}            /tmp/xtrinode-nodepool-default-infra.json

*** Test Cases ***
Operator Node Pool Defaults Gate Runtime Until Required Replicas Are Ready
    [Teardown]    Run Keywords    Run Keyword If Test Failed    Dump Debug    AND    Cleanup NodePool Contract Objects
    Cleanup NodePool Contract Objects
    Apply NodePool Default Runtime
    Wait Until Keyword Succeeds    180s    ${POLL_INTERVAL}    MachinePool Should Have Operator Defaults
    Wait Until Keyword Succeeds    180s    ${POLL_INTERVAL}    GCPManagedMachinePool Should Have Operator Defaults

    Sleep    25s
    Runtime Deployment Should Not Exist    ${NODEPOOL_DEFAULT_RUNTIME}    coordinator
    Runtime Deployment Should Not Exist    ${NODEPOOL_DEFAULT_RUNTIME}    worker

    Patch Fake MachinePool Status    2    2
    Wait Until Keyword Succeeds    180s    ${POLL_INTERVAL}    Runtime Deployment Should Exist    ${NODEPOOL_DEFAULT_RUNTIME}    coordinator

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

MachinePool Should Have Operator Defaults
    ${json}=    Kubectl Output    get    machinepools    ${NODEPOOL_DEFAULT_RUNTIME}-pool    -n    ${NAMESPACE}    -o    json
    Create File    ${NODEPOOL_MACHINEPOOL_FILE}    ${json}
    JQ Should Match    ${NODEPOOL_MACHINEPOOL_FILE}    .spec.replicas == 2 and .metadata.annotations["cluster-autoscaler/node-group-min-size"] == "2" and .metadata.annotations["cluster-autoscaler/node-group-max-size"] == "4"

GCPManagedMachinePool Should Have Operator Defaults
    ${json}=    Kubectl Output    get    gcpmanagedmachinepools    ${NODEPOOL_DEFAULT_RUNTIME}-pool    -n    ${NAMESPACE}    -o    json
    Create File    ${NODEPOOL_INFRA_FILE}    ${json}
    JQ Should Match    ${NODEPOOL_INFRA_FILE}    .spec.scaling.minCount == 2 and .spec.scaling.maxCount == 4 and .spec.diskSizeGB == 64

Patch Fake MachinePool Status
    [Arguments]    ${ready_replicas}    ${replicas}
    ${patch}=    Set Variable    {"status":{"readyReplicas":${ready_replicas},"replicas":${replicas}}}
    Command Should Succeed    kubectl    patch    machinepools    ${NODEPOOL_DEFAULT_RUNTIME}-pool    -n    ${NAMESPACE}    --subresource=status    --type=merge    -p    ${patch}

Runtime Deployment Should Not Exist
    [Arguments]    ${runtime}    ${component}
    ${result}=    Run Command Allow Failure    kubectl    get    deployment    trino-${runtime}-${component}    -n    ${NAMESPACE}
    Should Not Be Equal As Integers    ${result.rc}    0    msg=${result.stdout}

Runtime Deployment Should Exist
    [Arguments]    ${runtime}    ${component}
    Command Should Succeed    kubectl    get    deployment    trino-${runtime}-${component}    -n    ${NAMESPACE}

Cleanup NodePool Contract Objects
    Run Command Allow Failure    kubectl    patch    xtrinode/${NODEPOOL_DEFAULT_RUNTIME}    -n    ${NAMESPACE}    --type=merge    -p    {"metadata":{"finalizers":[]}}
    Run Command Allow Failure    kubectl    delete    xtrinode/${NODEPOOL_DEFAULT_RUNTIME}    -n    ${NAMESPACE}    --ignore-not-found=true    --wait=false
    Run Command Allow Failure    kubectl    delete    deployment,service,configmap,poddisruptionbudget,serviceaccount,horizontalpodautoscaler,scaledobject,triggerauthentication    -n    ${NAMESPACE}    -l    app.kubernetes.io/instance=${NODEPOOL_DEFAULT_RUNTIME}    --ignore-not-found=true    --wait=false
    Run Command Allow Failure    kubectl    delete    machinepools    ${NODEPOOL_DEFAULT_RUNTIME}-pool    -n    ${NAMESPACE}    --ignore-not-found=true    --wait=false
    Run Command Allow Failure    kubectl    delete    gcpmanagedmachinepools    ${NODEPOOL_DEFAULT_RUNTIME}-pool    -n    ${NAMESPACE}    --ignore-not-found=true    --wait=false
