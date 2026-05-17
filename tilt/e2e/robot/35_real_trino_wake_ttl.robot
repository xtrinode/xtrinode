*** Settings ***
Documentation       Live wake TTL coverage for API resume, autosuspend, KEDA, and gateway restore.
Resource            resources/local.resource
Suite Setup         Run Keywords    Ensure Local XTrinode Ready    AND    Start Local Port Forwards
Suite Teardown      Run Keywords    Restore Wake TTL Test State    AND    Stop Local Port Forwards
Test Teardown       Run Keyword If Test Failed    Dump Debug
Test Tags           local    k3d    smoke    lifecycle    wake    ttl    keda

*** Variables ***
${WAKE_TTL}                 60s
${WAKE_AUTOSUSPEND}         1s
${RESTORE_AUTOSUSPEND}      30m

*** Test Cases ***
API Resume Wake TTL Blocks Autosuspend Until Expiry
    TRY
        Clear API Server Leases
        Suspend Local XTrinode Through API
        Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    XTrinode Suspended State Should Be    true
        Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    Deployment Spec Replicas Should Equal    ${NAMESPACE}    trino-${XTRINODE_NAME}-worker    0

        Clear API Server Leases
        Resume Local XTrinode Through API With Wake TTL
        Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    XTrinode Suspended State Should Be    false
        Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    XTrinode Wake Min Workers Should Equal    1
        Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    ScaledObject Min Replicas Should Equal    ${NAMESPACE}    trino-${XTRINODE_NAME}-workers    1
        Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    Deployment Should Be Available    ${NAMESPACE}    trino-${XTRINODE_NAME}-worker    1

        Patch Local XTrinode AutoSuspendAfter    ${WAKE_AUTOSUSPEND}
        Force Local XTrinode Reconcile    wake-ttl-active
        Sleep    15s
        XTrinode Suspended State Should Be    false

        Wait Until Keyword Succeeds    150s    5s    XTrinode Wake Status Should Be Empty
        Force Local XTrinode Reconcile    wake-ttl-expired
        Wait Until Keyword Succeeds    180s    ${POLL_INTERVAL}    XTrinode Suspended State Should Be    true
    FINALLY
        Restore Wake TTL Test State
    END

*** Keywords ***
Clear API Server Leases
    Run Command Allow Failure    kubectl    delete    lease    -n    ${OPERATOR_NAMESPACE}    -l    app.kubernetes.io/name=xtrinode-operator,app.kubernetes.io/component=api-server    --ignore-not-found=true

Suspend Local XTrinode Through API
    ${status}=    HTTP Request To File    POST    http://127.0.0.1:${API_SERVER_PORT}/api/v1/runtimes/${NAMESPACE}/${XTRINODE_NAME}/suspend    /tmp/xtrinode-wake-suspend.json    {}
    Should Be Equal    ${status}    202

Resume Local XTrinode Through API With Wake TTL
    ${body}=    Set Variable    {"wakeMinWorkers":1,"wakeTTL":"${WAKE_TTL}"}
    ${status}=    HTTP Request To File    POST    http://127.0.0.1:${API_SERVER_PORT}/api/v1/runtimes/${NAMESPACE}/${XTRINODE_NAME}/resume    /tmp/xtrinode-wake-resume.json    ${body}    Content-Type: application/json
    Should Be Equal    ${status}    202

Resume Local XTrinode Through API Without Wake Override
    ${status}=    HTTP Request To File    POST    http://127.0.0.1:${API_SERVER_PORT}/api/v1/runtimes/${NAMESPACE}/${XTRINODE_NAME}/resume    /tmp/xtrinode-wake-restore-resume.json    {}    Content-Type: application/json
    Should Be Equal    ${status}    202

Patch Local XTrinode AutoSuspendAfter
    [Arguments]    ${duration}
    ${patch}=    Set Variable    {"spec":{"autoSuspendAfter":"${duration}"}}
    Command Should Succeed    kubectl    patch    xtrinode/${XTRINODE_NAME}    -n    ${NAMESPACE}    --type=merge    -p    ${patch}

Force Local XTrinode Reconcile
    [Arguments]    ${reason}
    Command Should Succeed    kubectl    annotate    xtrinode/${XTRINODE_NAME}    -n    ${NAMESPACE}    xtrinode.analytics.xtrinode.io/e2e-reconcile=${reason}    --overwrite

XTrinode Suspended State Should Be
    [Arguments]    ${expected}
    ${value}=    Kubectl Output    get    xtrinode    ${XTRINODE_NAME}    -n    ${NAMESPACE}    -o    jsonpath={.spec.suspended}
    ${value}=    Set Variable If    '${value}' == ''    false    ${value}
    Should Be Equal    ${value}    ${expected}

XTrinode Wake Min Workers Should Equal
    [Arguments]    ${expected}
    ${value}=    Kubectl Output    get    xtrinode    ${XTRINODE_NAME}    -n    ${NAMESPACE}    -o    jsonpath={.status.wake.minWorkers}
    Should Be Equal As Integers    ${value}    ${expected}
    ${expires_at}=    Kubectl Output    get    xtrinode    ${XTRINODE_NAME}    -n    ${NAMESPACE}    -o    jsonpath={.status.wake.expiresAt}
    Should Not Be Empty    ${expires_at}

XTrinode Wake Status Should Be Empty
    ${expires_at}=    Kubectl Output    get    xtrinode    ${XTRINODE_NAME}    -n    ${NAMESPACE}    -o    jsonpath={.status.wake.expiresAt}
    Should Be Empty    ${expires_at}

ScaledObject Min Replicas Should Equal
    [Arguments]    ${namespace}    ${scaledobject}    ${expected}
    ${replicas}=    Kubectl Output    get    scaledobject    ${scaledobject}    -n    ${namespace}    -o    jsonpath={.spec.minReplicaCount}
    Should Be Equal As Integers    ${replicas}    ${expected}

Restore Wake TTL Test State
    Patch Local XTrinode AutoSuspendAfter    ${RESTORE_AUTOSUSPEND}
    Clear API Server Leases
    ${suspended}=    Kubectl Output    get    xtrinode    ${XTRINODE_NAME}    -n    ${NAMESPACE}    -o    jsonpath={.spec.suspended}
    ${suspended}=    Set Variable If    '${suspended}' == ''    false    ${suspended}
    IF    '${suspended}' == 'true'
        Resume Local XTrinode Through API Without Wake Override
        Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    XTrinode Suspended State Should Be    false
    END
    Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    Deployment Should Be Available    ${NAMESPACE}    trino-${XTRINODE_NAME}-coordinator    1
    Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    Deployment Should Be Available    ${NAMESPACE}    trino-${XTRINODE_NAME}-worker    1
    Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    Gateway Route Should Be Registered
