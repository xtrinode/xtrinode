*** Settings ***
Documentation       XTrinode resource contracts for local real-Trino/KEDA deployment.
Resource            resources/local.resource
Suite Setup         Ensure Local XTrinode Ready
Test Tags           local    k3d    contracts    xtrinode
Test Teardown       Run Keyword If Test Failed    Dump Debug

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
    Should Match Regexp    ${apply.stdout}    exactly one connector field must be set|at most 1 properties|must have at most 1 items

Gateway Route Config Contains Local Backend
    ${routes}=    Kubectl Output    get    configmap    trino-gateway-routes    -n    ${GATEWAY_NAMESPACE}    -o    ${GATEWAY_ROUTES_OUTPUT}
    Should Contain    ${routes}    coordinatorURL: http://trino-${XTRINODE_NAME}.${NAMESPACE}.svc.cluster.local:8080
    Should Contain    ${routes}    header: ${XTRINODE_NAME}
    Should Contain    ${routes}    state: RUNNING

*** Keywords ***
Cleanup KEDA Admission Contract Objects
    Run Command Allow Failure    kubectl    delete    scaledobject    invalid-memory-zero-contract    -n    ${NAMESPACE}    --ignore-not-found=true
    Run Command Allow Failure    kubectl    delete    deployment    invalid-memory-zero-target    -n    ${NAMESPACE}    --ignore-not-found=true

Cleanup XTrinodeCatalog Admission Contract Objects
    Run Command Allow Failure    kubectl    delete    xtrinodecatalog    invalid-catalog-webhook-contract    -n    ${NAMESPACE}    --ignore-not-found=true
