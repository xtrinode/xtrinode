*** Settings ***
Documentation       Live cleanup coverage for interrupted XTrinode lifecycle transitions.
...                 Requires the local k3d stack, KEDA, API server, gateway, Redis, Postgres, kubectl, curl, and jq.
Resource            resources/local.resource
Suite Setup         Setup Lifecycle Cleanup Interruption Suite
Suite Teardown      Teardown Lifecycle Cleanup Interruption Suite
Test Teardown       Run Keywords    Restore Operator    AND    Run Keyword If Test Failed    Dump Debug
Test Tags           local    k3d    lifecycle-cleanup    lifecycle    gateway    keda    interruption

*** Variables ***
${OPERATOR_DEPLOYMENT}              xtrinode-operator
${COORDINATOR_DEPLOYMENT}           trino-${XTRINODE_NAME}-coordinator
${WORKER_DEPLOYMENT}                trino-${XTRINODE_NAME}-worker
${SCALEDOBJECT}                     trino-${XTRINODE_NAME}-workers
${CLEANUP_XTRINODE_NAME}            cleanup-interrupt
${CLEANUP_ROUTE_GROUP}              cleanup-interrupt
${ROUTES_PATCH_FILE}                /tmp/xtrinode-lifecycle-cleanup-routes-patch.json
${TEMP_XTRINODE_FILE}               /tmp/xtrinode-lifecycle-cleanup-xtrinode.json
${LEASES_JSON_FILE}                 /tmp/xtrinode-lifecycle-cleanup-leases.json
${XTRINODE_JSON_FILE}               /tmp/xtrinode-lifecycle-cleanup-runtime.json
${RESUME_REQUESTED_ANNOTATION}      xtrinode.analytics.xtrinode.io/resume-requested
${RESUME_REQUESTED_AT_ANNOTATION}   xtrinode.analytics.xtrinode.io/resume-requested-at
${SUSPEND_REQUESTED_ANNOTATION}     xtrinode.analytics.xtrinode.io/suspend-requested
${SUSPEND_REQUESTED_AT_ANNOTATION}  xtrinode.analytics.xtrinode.io/suspend-requested-at
${WAKE_MIN_WORKERS_ANNOTATION}      xtrinode.analytics.xtrinode.io/wake-min-workers
${WAKE_TTL_ANNOTATION}              xtrinode.analytics.xtrinode.io/wake-ttl

*** Test Cases ***
Gateway Route Registration Repairs After Operator Interruption
    Scale Operator To Zero
    Set Gateway Routes To Empty
    Gateway Route Should Not Contain Runtime    ${XTRINODE_NAME}

    Restore Operator
    Force Runtime Reconcile    interrupted-route-registration
    Wait Until Keyword Succeeds    180s    ${POLL_INTERVAL}    Gateway Route Should Be Registered
    Wait For Gateway Backend Ready

Gateway Route Deregistration Completes After Operator Interruption
    Ensure Interrupted Cleanup Runtime Route
    Scale Operator To Zero
    Command Should Succeed    kubectl    delete    xtrinode/${CLEANUP_XTRINODE_NAME}    -n    ${NAMESPACE}    --wait=false
    Runtime Deletion Should Be Pending    ${CLEANUP_XTRINODE_NAME}
    Gateway Route Should Contain Runtime With State    ${CLEANUP_XTRINODE_NAME}    PAUSED

    Restore Operator
    Wait Until Keyword Succeeds    240s    ${POLL_INTERVAL}    Runtime Should Not Exist    ${CLEANUP_XTRINODE_NAME}
    Wait Until Keyword Succeeds    60s    ${POLL_INTERVAL}    Gateway Route Should Not Contain Runtime    ${CLEANUP_XTRINODE_NAME}

KEDA Worker Handoff Recovers After Operator Interruption
    Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    ScaledObject Should Be Ready
    Scale Operator To Zero
    Command Should Succeed    kubectl    delete    scaledobject/${SCALEDOBJECT}    -n    ${NAMESPACE}    --ignore-not-found=true    --wait=false
    Command Should Succeed    kubectl    scale    deployment/${WORKER_DEPLOYMENT}    -n    ${NAMESPACE}    --replicas=0

    Restore Operator
    Force Runtime Reconcile    interrupted-keda-handoff
    Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    ScaledObject Should Be Ready
    Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    ScaledObject Min Replicas Should Equal    1
    ScaledObject Max Replicas Should Equal    ${NAMESPACE}    ${SCALEDOBJECT}    1
    Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    Deployment Should Be Available    ${NAMESPACE}    ${WORKER_DEPLOYMENT}    1
    Deployment Spec Replicas Should Equal    ${NAMESPACE}    ${WORKER_DEPLOYMENT}    1

Suspend Resume Retry Cleans Lifecycle State After Operator Interruption
    Clear API Server Leases
    Scale Operator To Zero
    Suspend Local Runtime Through API Should Fail And Release Lease
    Runtime Command Annotations Should Be Clear
    Runtime Lease Should Not Exist For Key Type    suspend

    Restore Operator
    Suspend Local Runtime Through API
    Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    XTrinode Suspended State Should Be    true
    Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    Runtime Command Annotations Should Be Clear
    Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    Deployment Spec Replicas Should Equal    ${NAMESPACE}    ${COORDINATOR_DEPLOYMENT}    0
    Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    Deployment Spec Replicas Should Equal    ${NAMESPACE}    ${WORKER_DEPLOYMENT}    0
    Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    Gateway Route Should Contain Runtime With State    ${XTRINODE_NAME}    PAUSED

    Clear API Server Leases
    Scale Operator To Zero
    Resume Local Runtime Through API Should Fail And Release Lease
    Runtime Command Annotations Should Be Clear
    Runtime Lease Should Not Exist For Key Type    runtime

    Restore Operator
    Resume Local Runtime Through API With Wake Floor
    Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    XTrinode Suspended State Should Be    false
    Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    Runtime Command Annotations Should Be Clear
    Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    ScaledObject Should Be Ready
    Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    Deployment Should Be Available    ${NAMESPACE}    ${COORDINATOR_DEPLOYMENT}    1
    Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    Deployment Should Be Available    ${NAMESPACE}    ${WORKER_DEPLOYMENT}    1
    Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    Gateway Route Should Be Registered
    Wait For Gateway Backend Ready

Gateway Rejects New Queries During Suspend And Resume Transitions
    TRY
        Ensure Local Runtime Resumed
        Clear API Server Leases
        Wait For Gateway Backend Ready

        Suspend Local Runtime Through API
        Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    Gateway Route Should Contain Runtime With State    ${XTRINODE_NAME}    PAUSED
        Gateway Statement Should Return Status    /tmp/xtrinode-lifecycle-suspend-transition-query.json    SELECT 1    503
        JQ Should Match    /tmp/xtrinode-lifecycle-suspend-transition-query.json    (.error // "") | test("resum|retry|suspend|unavailable")
        Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    XTrinode Suspended State Should Be    false
        Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    Gateway Route Should Be Registered
        Wait For Gateway Backend Ready

        Clear API Server Leases
        Suspend Local Runtime Through API
        Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    XTrinode Suspended State Should Be    true
        Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    Deployment Spec Replicas Should Equal    ${NAMESPACE}    ${COORDINATOR_DEPLOYMENT}    0
        Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    Deployment Spec Replicas Should Equal    ${NAMESPACE}    ${WORKER_DEPLOYMENT}    0
        Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    Gateway Route Should Contain Runtime With State    ${XTRINODE_NAME}    PAUSED

        Gateway Statement Should Return Status    /tmp/xtrinode-lifecycle-resume-trigger-query.json    SELECT 1    503
        JQ Should Match    /tmp/xtrinode-lifecycle-resume-trigger-query.json    .triggered == true
        Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    Gateway Route Should Contain Runtime With State    ${XTRINODE_NAME}    RESUMING
        Gateway Statement Should Return Status    /tmp/xtrinode-lifecycle-resuming-transition-query.json    SELECT 1    503
        JQ Should Match    /tmp/xtrinode-lifecycle-resuming-transition-query.json    (.error // "") | test("resum|retry|unavailable")
        Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    Gateway Route Should Be Registered
        Wait For Gateway Backend Ready
        Gateway Statement Should Return Status    /tmp/xtrinode-lifecycle-after-resume-query.json    SELECT 1    200
    FINALLY
        Ensure Local Runtime Resumed
        Clear API Server Leases
    END

*** Keywords ***
Setup Lifecycle Cleanup Interruption Suite
    Ensure Local XTrinode Ready
    Save Original Gateway Routes
    Start Local Port Forwards
    Clear API Server Leases

Teardown Lifecycle Cleanup Interruption Suite
    Run Keyword And Ignore Error    Restore Operator
    Run Keyword And Ignore Error    Cleanup Interrupted Runtime
    Run Keyword And Ignore Error    Restore Original Gateway Routes
    Run Keyword And Ignore Error    Clear API Server Leases
    Run Keyword And Ignore Error    Ensure Local Runtime Resumed
    Stop Local Port Forwards

Scale Operator To Zero
    Command Should Succeed    kubectl    scale    deployment/${OPERATOR_DEPLOYMENT}    -n    ${OPERATOR_NAMESPACE}    --replicas=0
    Wait Until Keyword Succeeds    120s    2s    Deployment Available Replicas Should Equal    ${OPERATOR_NAMESPACE}    ${OPERATOR_DEPLOYMENT}    0

Restore Operator
    Command Should Succeed    kubectl    scale    deployment/${OPERATOR_DEPLOYMENT}    -n    ${OPERATOR_NAMESPACE}    --replicas=1
    Command Should Succeed    kubectl    rollout    status    deployment/${OPERATOR_DEPLOYMENT}    -n    ${OPERATOR_NAMESPACE}    --timeout=180s
    Wait Until Keyword Succeeds    180s    2s    Deployment Should Be Available    ${OPERATOR_NAMESPACE}    ${OPERATOR_DEPLOYMENT}    1

Save Original Gateway Routes
    ${routes}=    Kubectl Output    get    configmap    trino-gateway-routes    -n    ${GATEWAY_NAMESPACE}    -o    ${GATEWAY_ROUTES_OUTPUT}
    Set Suite Variable    ${ORIGINAL_GATEWAY_ROUTES}    ${routes}

Restore Original Gateway Routes
    Patch Gateway Routes ConfigMap    ${ORIGINAL_GATEWAY_ROUTES}
    Wait Until Keyword Succeeds    60s    ${POLL_INTERVAL}    Gateway Route Should Be Registered

Set Gateway Routes To Empty
    ${routes}=    Catenate    SEPARATOR=\n    routes: []    ${EMPTY}
    Patch Gateway Routes ConfigMap    ${routes}

Patch Gateway Routes ConfigMap
    [Arguments]    ${routes}
    ${patch}=    Run Process    jq    -n    --arg    routes    ${routes}    {"data":{"routes.yaml":$routes}}    stdout=${ROUTES_PATCH_FILE}    stderr=STDOUT
    Should Be Equal As Integers    ${patch.rc}    0    msg=${patch.stdout}
    Command Should Succeed    kubectl    patch    configmap    trino-gateway-routes    -n    ${GATEWAY_NAMESPACE}    --type=merge    --patch-file    ${ROUTES_PATCH_FILE}

Gateway Route Should Not Contain Runtime
    [Arguments]    ${runtime}
    ${routes}=    Kubectl Output    get    configmap    trino-gateway-routes    -n    ${GATEWAY_NAMESPACE}    -o    ${GATEWAY_ROUTES_OUTPUT}
    Should Not Contain    ${routes}    name: ${runtime}

Gateway Route Should Contain Runtime With State
    [Arguments]    ${runtime}    ${state}
    ${routes}=    Kubectl Output    get    configmap    trino-gateway-routes    -n    ${GATEWAY_NAMESPACE}    -o    ${GATEWAY_ROUTES_OUTPUT}
    Should Contain    ${routes}    name: ${runtime}
    Should Contain    ${routes}    state: ${state}

Ensure Interrupted Cleanup Runtime Route
    Cleanup Interrupted Runtime
    ${json}=    Set Variable    {"apiVersion":"analytics.xtrinode.io/v1","kind":"XTrinode","metadata":{"name":"${CLEANUP_XTRINODE_NAME}","namespace":"${NAMESPACE}","labels":{"test.xtrinode.io/contract":"lifecycle-cleanup"}},"spec":{"size":"xs","minWorkers":1,"maxWorkers":1,"suspended":true,"autoSuspendAfter":"30m","routing":{"header":"X-Trino-XTrinode=${CLEANUP_XTRINODE_NAME}","routingGroup":"${CLEANUP_ROUTE_GROUP}"}}}
    Create File    ${TEMP_XTRINODE_FILE}    ${json}
    Command Should Succeed    kubectl    apply    -f    ${TEMP_XTRINODE_FILE}
    Wait Until Keyword Succeeds    180s    ${POLL_INTERVAL}    Gateway Route Should Contain Runtime With State    ${CLEANUP_XTRINODE_NAME}    PAUSED

Cleanup Interrupted Runtime
    Run Command Allow Failure    kubectl    patch    xtrinode/${CLEANUP_XTRINODE_NAME}    -n    ${NAMESPACE}    --type=merge    -p    {"metadata":{"finalizers":[]}}
    Run Command Allow Failure    kubectl    delete    xtrinode/${CLEANUP_XTRINODE_NAME}    -n    ${NAMESPACE}    --ignore-not-found=true    --wait=false

Runtime Deletion Should Be Pending
    [Arguments]    ${runtime}
    ${json}=    Kubectl Output    get    xtrinode/${runtime}    -n    ${NAMESPACE}    -o    json
    Create File    ${XTRINODE_JSON_FILE}    ${json}
    JQ Should Match    ${XTRINODE_JSON_FILE}    .metadata.deletionTimestamp != null

Runtime Should Not Exist
    [Arguments]    ${runtime}
    ${result}=    Run Command Allow Failure    kubectl    get    xtrinode/${runtime}    -n    ${NAMESPACE}
    Should Not Be Equal As Integers    ${result.rc}    0    msg=${result.stdout}

Force Runtime Reconcile
    [Arguments]    ${reason}
    Command Should Succeed    kubectl    annotate    xtrinode/${XTRINODE_NAME}    -n    ${NAMESPACE}    xtrinode.analytics.xtrinode.io/lifecycle-cleanup-reconcile=${reason}    --overwrite

ScaledObject Should Be Ready
    Command Should Succeed    kubectl    wait    scaledobject/${SCALEDOBJECT}    -n    ${NAMESPACE}    --for=condition=Ready=True    --timeout=30s

ScaledObject Min Replicas Should Equal
    [Arguments]    ${expected}
    ${replicas}=    Kubectl Output    get    scaledobject    ${SCALEDOBJECT}    -n    ${NAMESPACE}    -o    jsonpath={.spec.minReplicaCount}
    Should Be Equal As Integers    ${replicas}    ${expected}

Clear API Server Leases
    Run Command Allow Failure    kubectl    delete    lease    -n    ${OPERATOR_NAMESPACE}    -l    app.kubernetes.io/name=xtrinode-operator,app.kubernetes.io/component=api-server    --ignore-not-found=true

Suspend Local Runtime Through API
    ${status}=    HTTP Request To File    POST    http://127.0.0.1:${API_SERVER_PORT}/api/v1/runtimes/${NAMESPACE}/${XTRINODE_NAME}/suspend    /tmp/xtrinode-lifecycle-cleanup-suspend.json    {}
    Should Be Equal    ${status}    202

Suspend Local Runtime Through API Should Fail And Release Lease
    ${status}=    HTTP Request To File    POST    http://127.0.0.1:${API_SERVER_PORT}/api/v1/runtimes/${NAMESPACE}/${XTRINODE_NAME}/suspend    /tmp/xtrinode-lifecycle-cleanup-suspend.json    {}
    Should Be Equal    ${status}    500
    JQ Should Match    /tmp/xtrinode-lifecycle-cleanup-suspend.json    .code == "UPDATE_FAILED"
    Wait Until Keyword Succeeds    30s    2s    Runtime Lease Should Not Exist For Key Type    suspend

Resume Local Runtime Through API With Wake Floor
    ${body}=    Set Variable    {"wakeMinWorkers":1,"wakeTTL":"120s"}
    ${status}=    HTTP Request To File    POST    http://127.0.0.1:${API_SERVER_PORT}/api/v1/runtimes/${NAMESPACE}/${XTRINODE_NAME}/resume    /tmp/xtrinode-lifecycle-cleanup-resume.json    ${body}    Content-Type: application/json
    Should Be Equal    ${status}    202

Resume Local Runtime Through API Should Fail And Release Lease
    ${body}=    Set Variable    {"wakeMinWorkers":1,"wakeTTL":"120s"}
    ${status}=    HTTP Request To File    POST    http://127.0.0.1:${API_SERVER_PORT}/api/v1/runtimes/${NAMESPACE}/${XTRINODE_NAME}/resume    /tmp/xtrinode-lifecycle-cleanup-resume.json    ${body}    Content-Type: application/json
    Should Be Equal    ${status}    500
    JQ Should Match    /tmp/xtrinode-lifecycle-cleanup-resume.json    .code == "UPDATE_FAILED"
    Wait Until Keyword Succeeds    30s    2s    Runtime Lease Should Not Exist For Key Type    runtime

XTrinode Suspended State Should Be
    [Arguments]    ${expected}
    ${value}=    Kubectl Output    get    xtrinode    ${XTRINODE_NAME}    -n    ${NAMESPACE}    -o    jsonpath={.spec.suspended}
    ${value}=    Set Variable If    '${value}' == ''    false    ${value}
    Should Be Equal    ${value}    ${expected}

Runtime Annotation Should Equal
    [Arguments]    ${annotation}    ${expected}
    ${value}=    Runtime Annotation Value    ${annotation}
    Should Be Equal    ${value}    ${expected}

Runtime Annotation Should Be Empty
    [Arguments]    ${annotation}
    ${value}=    Runtime Annotation Value    ${annotation}
    Should Be Empty    ${value}

Runtime Annotation Value
    [Arguments]    ${annotation}
    ${json}=    Kubectl Output    get    xtrinode/${XTRINODE_NAME}    -n    ${NAMESPACE}    -o    json
    Create File    ${XTRINODE_JSON_FILE}    ${json}
    ${result}=    Run Process    jq    -r    --arg    key    ${annotation}    .metadata.annotations[$key] // ""    ${XTRINODE_JSON_FILE}    stderr=STDOUT
    Log    ${result.stdout}
    Should Be Equal As Integers    ${result.rc}    0    msg=${result.stdout}
    ${value}=    Strip String    ${result.stdout}
    RETURN    ${value}

Runtime Command Annotations Should Be Clear
    Runtime Annotation Should Be Empty    ${RESUME_REQUESTED_ANNOTATION}
    Runtime Annotation Should Be Empty    ${RESUME_REQUESTED_AT_ANNOTATION}
    Runtime Annotation Should Be Empty    ${SUSPEND_REQUESTED_ANNOTATION}
    Runtime Annotation Should Be Empty    ${SUSPEND_REQUESTED_AT_ANNOTATION}
    Runtime Annotation Should Be Empty    ${WAKE_MIN_WORKERS_ANNOTATION}
    Runtime Annotation Should Be Empty    ${WAKE_TTL_ANNOTATION}

Runtime Lease Should Exist For Key Type
    [Arguments]    ${key_type}
    ${leases}=    Kubectl Output    get    lease    -n    ${OPERATOR_NAMESPACE}    -l    xtrinode.io/lease-key-type=${key_type}    -o    json
    Create File    ${LEASES_JSON_FILE}    ${leases}
    JQ Should Match    ${LEASES_JSON_FILE}    any(.items[]?; .metadata.annotations["xtrinode.io/lease-key"] == $key)    --arg    key    rt/${NAMESPACE}/${XTRINODE_NAME}

Runtime Lease Should Not Exist For Key Type
    [Arguments]    ${key_type}
    ${leases}=    Kubectl Output    get    lease    -n    ${OPERATOR_NAMESPACE}    -l    xtrinode.io/lease-key-type=${key_type}    -o    json
    Create File    ${LEASES_JSON_FILE}    ${leases}
    JQ Should Match    ${LEASES_JSON_FILE}    all(.items[]?; .metadata.annotations["xtrinode.io/lease-key"] != $key)    --arg    key    rt/${NAMESPACE}/${XTRINODE_NAME}

Ensure Local Runtime Resumed
    ${result}=    Run Command Allow Failure    kubectl    get    xtrinode/${XTRINODE_NAME}    -n    ${NAMESPACE}
    IF    ${result.rc} != 0
        Ensure Local XTrinode Ready
        RETURN
    END
    ${suspended}=    Kubectl Output    get    xtrinode    ${XTRINODE_NAME}    -n    ${NAMESPACE}    -o    jsonpath={.spec.suspended}
    ${suspended}=    Set Variable If    '${suspended}' == ''    false    ${suspended}
    IF    '${suspended}' == 'true'
        Clear API Server Leases
        Resume Local Runtime Through API With Wake Floor
    END
    Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    XTrinode Suspended State Should Be    false
    Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    Deployment Should Be Available    ${NAMESPACE}    ${COORDINATOR_DEPLOYMENT}    1
    Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    Deployment Should Be Available    ${NAMESPACE}    ${WORKER_DEPLOYMENT}    1
    Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    Gateway Route Should Be Registered
