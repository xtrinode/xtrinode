*** Settings ***
Documentation       Namespace guardrail contracts for same-namespace XTrinode create/delete races.
Resource            resources/local.resource
Suite Setup         Ensure Namespace Guardrail Contract Prerequisites
Suite Teardown      Cleanup Namespace Guardrail Contract Objects
Test Tags           local    k3d    contracts    xtrinode    namespace-guardrails
Test Teardown       Run Keyword If Test Failed    Dump Namespace Guardrail Debug

*** Variables ***
${GUARDRAIL_NAMESPACE}       team-guardrails
${GUARDRAIL_A}               guardrail-a
${GUARDRAIL_B}               guardrail-b
${GUARDRAIL_QUOTA}           xtrinode-namespace-quota
${GUARDRAIL_LIMITRANGE}      xtrinode-namespace-limits
${DRAIN_STARTED_ANNOTATION}  xtrinode.analytics.xtrinode.io/drain-started-at
${DRAIN_ELAPSED_TIMESTAMP}   2000-01-01T00:00:00Z

*** Test Cases ***
Shared Namespace Guardrails Recalculate Through Deletion Finalizers
    Wait Until Keyword Succeeds    180s    2s    Namespace Guardrail Quota Should Equal    8    32Gi
    Namespace Guardrail LimitRange Should Equal    2    8Gi
    Delete Guardrail XTrinode Through Finalizer    ${GUARDRAIL_A}
    Wait Until Keyword Succeeds    180s    2s    Namespace Guardrail Quota Should Equal    4    16Gi
    Namespace Guardrail LimitRange Should Equal    2    8Gi
    Delete Guardrail XTrinode Through Finalizer    ${GUARDRAIL_B}
    Wait Until Keyword Succeeds    120s    2s    Namespace Guardrail Resource Should Not Exist    resourcequota    ${GUARDRAIL_QUOTA}
    Namespace Guardrail Resource Should Not Exist    limitrange    ${GUARDRAIL_LIMITRANGE}

*** Keywords ***
Ensure Namespace Guardrail Contract Prerequisites
    Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    Deployment Should Be Available    ${OPERATOR_NAMESPACE}    xtrinode-operator    1
    Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    Deployment Should Be Available    ${GATEWAY_NAMESPACE}    xtrinode-gateway    1
    Cleanup Namespace Guardrail Contract Objects
    Create Guardrail Namespace If Missing
    Apply Guardrail XTrinode    ${GUARDRAIL_A}
    Apply Guardrail XTrinode    ${GUARDRAIL_B}

Create Guardrail Namespace If Missing
    ${result}=    Run Process    kubectl    create    namespace    ${GUARDRAIL_NAMESPACE}    stderr=STDOUT
    Log    ${result.stdout}
    IF    ${result.rc} != 0
        Should Contain    ${result.stdout}    AlreadyExists
    END

Apply Guardrail XTrinode
    [Arguments]    ${name}
    ${manifest}=    Set Variable    /tmp/xtrinode-namespace-guardrail-${name}.json
    ${json}=    Set Variable    {"apiVersion":"analytics.xtrinode.io/v1","kind":"XTrinode","metadata":{"name":"${name}","namespace":"${GUARDRAIL_NAMESPACE}","labels":{"test.xtrinode.io/contract":"namespace-guardrails"}},"spec":{"size":"xs","minWorkers":0,"maxWorkers":1,"suspended":false,"routing":{"header":"X-Trino-XTrinode=${GUARDRAIL_NAMESPACE}/${name}","routingGroup":"${name}"},"valuesOverlay":{"image":{"repository":"${TRINO_IMAGE_REPOSITORY}","tag":"${TRINO_IMAGE_TAG}","pullPolicy":"IfNotPresent"},"server":{"workers":0},"coordinator":{"resources":{"requests":{"cpu":"50m","memory":"512Mi"},"limits":{"cpu":"250m","memory":"768Mi"}}},"worker":{"resources":{"requests":{"cpu":"50m","memory":"512Mi"},"limits":{"cpu":"250m","memory":"768Mi"}}}}}}
    Create File    ${manifest}    ${json}
    Command Should Succeed    kubectl    apply    -f    ${manifest}

Namespace Guardrail Quota Should Equal
    [Arguments]    ${cpu}    ${memory}
    ${quota}=    Kubectl Output    get    resourcequota    ${GUARDRAIL_QUOTA}    -n    ${GUARDRAIL_NAMESPACE}    -o    json
    ${quota_file}=    Set Variable    /tmp/xtrinode-namespace-guardrail-quota.json
    Create File    ${quota_file}    ${quota}
    JQ Should Match    ${quota_file}    .spec.hard.cpu == $cpu and .spec.hard.memory == $memory    --arg    cpu    ${cpu}    --arg    memory    ${memory}

Namespace Guardrail LimitRange Should Equal
    [Arguments]    ${cpu}    ${memory}
    ${limitrange}=    Kubectl Output    get    limitrange    ${GUARDRAIL_LIMITRANGE}    -n    ${GUARDRAIL_NAMESPACE}    -o    json
    ${limitrange_file}=    Set Variable    /tmp/xtrinode-namespace-guardrail-limitrange.json
    Create File    ${limitrange_file}    ${limitrange}
    JQ Should Match    ${limitrange_file}    .spec.limits[0].default.cpu == $cpu and .spec.limits[0].default.memory == $memory and .spec.limits[0].max.cpu == $cpu and .spec.limits[0].max.memory == $memory    --arg    cpu    ${cpu}    --arg    memory    ${memory}

Namespace Guardrail Resource Should Not Exist
    [Arguments]    ${kind}    ${name}
    ${result}=    Run Command Allow Failure    kubectl    get    ${kind}    ${name}    -n    ${GUARDRAIL_NAMESPACE}
    Should Not Be Equal As Integers    ${result.rc}    0

Delete Guardrail XTrinode Through Finalizer
    [Arguments]    ${name}
    Command Should Succeed    kubectl    delete    xtrinode/${name}    -n    ${GUARDRAIL_NAMESPACE}    --wait=false
    Wait Until Keyword Succeeds    60s    2s    Guardrail XTrinode Drain Annotation Should Exist    ${name}
    ${patch}=    Set Variable    {"metadata":{"annotations":{"${DRAIN_STARTED_ANNOTATION}":"${DRAIN_ELAPSED_TIMESTAMP}"}}}
    Command Should Succeed    kubectl    patch    xtrinode/${name}    -n    ${GUARDRAIL_NAMESPACE}    --type=merge    -p    ${patch}
    Command Should Succeed    kubectl    wait    xtrinode/${name}    -n    ${GUARDRAIL_NAMESPACE}    --for=delete    --timeout=180s

Guardrail XTrinode Drain Annotation Should Exist
    [Arguments]    ${name}
    ${annotation}=    Kubectl Output    get    xtrinode/${name}    -n    ${GUARDRAIL_NAMESPACE}    -o    jsonpath={.metadata.annotations.xtrinode\\.analytics\\.xtrinode\\.io/drain-started-at}
    Should Not Be Empty    ${annotation}

Cleanup Namespace Guardrail Contract Objects
    Run Command Allow Failure    kubectl    patch    xtrinode/${GUARDRAIL_A}    -n    ${GUARDRAIL_NAMESPACE}    --type=merge    -p    {"metadata":{"finalizers":[]}}
    Run Command Allow Failure    kubectl    patch    xtrinode/${GUARDRAIL_B}    -n    ${GUARDRAIL_NAMESPACE}    --type=merge    -p    {"metadata":{"finalizers":[]}}
    Run Command Allow Failure    kubectl    delete    xtrinode/${GUARDRAIL_A}    -n    ${GUARDRAIL_NAMESPACE}    --wait=false    --ignore-not-found=true
    Run Command Allow Failure    kubectl    delete    xtrinode/${GUARDRAIL_B}    -n    ${GUARDRAIL_NAMESPACE}    --wait=false    --ignore-not-found=true
    Run Command Allow Failure    kubectl    wait    xtrinode/${GUARDRAIL_A}    -n    ${GUARDRAIL_NAMESPACE}    --for=delete    --timeout=120s
    Run Command Allow Failure    kubectl    wait    xtrinode/${GUARDRAIL_B}    -n    ${GUARDRAIL_NAMESPACE}    --for=delete    --timeout=120s
    Run Command Allow Failure    kubectl    delete    deployment,service,configmap,poddisruptionbudget,serviceaccount,horizontalpodautoscaler,scaledobject,triggerauthentication    -n    ${GUARDRAIL_NAMESPACE}    -l    test.xtrinode.io/contract=namespace-guardrails    --ignore-not-found=true    --wait=false
    Run Command Allow Failure    kubectl    delete    deployment,service,configmap,poddisruptionbudget,serviceaccount,horizontalpodautoscaler,scaledobject,triggerauthentication    -n    ${GUARDRAIL_NAMESPACE}    -l    app.kubernetes.io/instance=${GUARDRAIL_A}    --ignore-not-found=true    --wait=false
    Run Command Allow Failure    kubectl    delete    deployment,service,configmap,poddisruptionbudget,serviceaccount,horizontalpodautoscaler,scaledobject,triggerauthentication    -n    ${GUARDRAIL_NAMESPACE}    -l    app.kubernetes.io/instance=${GUARDRAIL_B}    --ignore-not-found=true    --wait=false
    Run Command Allow Failure    kubectl    delete    resourcequota    ${GUARDRAIL_QUOTA}    -n    ${GUARDRAIL_NAMESPACE}    --ignore-not-found=true
    Run Command Allow Failure    kubectl    delete    limitrange    ${GUARDRAIL_LIMITRANGE}    -n    ${GUARDRAIL_NAMESPACE}    --ignore-not-found=true
    Remove Namespace Guardrail Routes From Gateway ConfigMap

Remove Namespace Guardrail Routes From Gateway ConfigMap
    ${routes_result}=    Run Command Allow Failure    kubectl    get    configmap    trino-gateway-routes    -n    ${GATEWAY_NAMESPACE}    -o    ${GATEWAY_ROUTES_OUTPUT}
    IF    ${routes_result.rc} != 0
        RETURN
    END
    ${routes_file}=    Set Variable    /tmp/xtrinode-namespace-guardrail-routes.yaml
    ${filtered_file}=    Set Variable    /tmp/xtrinode-namespace-guardrail-routes-filtered.yaml
    ${patch_file}=    Set Variable    /tmp/xtrinode-namespace-guardrail-routes-patch.json
    Create File    ${routes_file}    ${routes_result.stdout}
    ${filtered}=    Command Should Succeed    yq    -y    (.routes // []) |= map(select(.routingGroup != "guardrail-a" and .routingGroup != "guardrail-b"))    ${routes_file}
    Create File    ${filtered_file}    ${filtered}
    ${patch}=    Command Should Succeed    jq    -n    --arg    routes    ${filtered}    [{"op":"replace","path":"/data/routes.yaml","value":$routes}]
    Create File    ${patch_file}    ${patch}
    Command Should Succeed    kubectl    patch    configmap    trino-gateway-routes    -n    ${GATEWAY_NAMESPACE}    --type=json    --patch-file    ${patch_file}

Dump Namespace Guardrail Debug
    Dump Debug
    Run Command Allow Failure    kubectl    get    xtrinode    -n    ${GUARDRAIL_NAMESPACE}    -o    yaml
    Run Command Allow Failure    kubectl    get    resourcequota,limitrange    -n    ${GUARDRAIL_NAMESPACE}    -o    yaml
    Run Command Allow Failure    kubectl    get    events    -n    ${GUARDRAIL_NAMESPACE}    --sort-by=.lastTimestamp
