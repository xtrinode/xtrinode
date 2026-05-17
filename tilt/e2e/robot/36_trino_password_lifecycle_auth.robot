*** Settings ***
Documentation       Live PASSWORD-auth Trino lifecycle coverage for internal control calls.
Resource            resources/local.resource
Suite Setup         Run Keywords    Ensure Password Lifecycle Contract Ready    AND    Start Local Port Forwards    AND    Start Password Lifecycle Coordinator Port Forward
Suite Teardown      Run Keywords    Stop Password Lifecycle Port Forwards    AND    Stop Local Port Forwards    AND    Cleanup Password Lifecycle Contract Objects
Test Teardown       Run Keyword If Test Failed    Dump Password Lifecycle Debug
Test Tags           local    k3d    smoke    lifecycle    auth    trino

*** Variables ***
${AUTH_NAMESPACE}          team-auth-lifecycle
${AUTH_XTRINODE}           auth-lifecycle
${AUTH_CONTROL_SECRET}     trino-control-auth
${AUTH_CONTROL_USER}       xtrinode-operator
${AUTH_CONTROL_PASSWORD}   local-control-password
${AUTH_PASSWORD_HASH}      $2b$12$LI6qmfhyOpSIXjwoEFwY/u/hMFM8iq4Ep0BVc5S4NMcbEMY43ofOS
${AUTH_INTERNAL_SECRET}    0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
${AUTH_COORDINATOR_PORT}   18182

*** Test Cases ***
Password Authenticated Trino Lifecycle Control Works
    Direct Trino Query List Without Password Should Return    401
    Direct Trino Query List With Control Password Should Return    200
    Worker Password Lifecycle Resources Should Be Wired

    Suspend Password Lifecycle XTrinode Through API
    Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    Password Lifecycle XTrinode Suspended State Should Be    true
    Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    Deployment Spec Replicas Should Equal    ${AUTH_NAMESPACE}    trino-${AUTH_XTRINODE}-worker    0

    Resume Password Lifecycle XTrinode Through API
    Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    Password Lifecycle XTrinode Suspended State Should Be    false
    Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    Deployment Should Be Available    ${AUTH_NAMESPACE}    trino-${AUTH_XTRINODE}-worker    1

*** Keywords ***
Ensure Password Lifecycle Contract Ready
    Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    Deployment Should Be Available    ${OPERATOR_NAMESPACE}    xtrinode-operator    1
    Create Password Lifecycle Namespace If Missing
    Cleanup Password Lifecycle Contract Objects
    Apply Password Lifecycle Control Secret
    Apply Password Lifecycle XTrinode
    Command Should Succeed    kubectl    wait    xtrinode/${AUTH_XTRINODE}    -n    ${AUTH_NAMESPACE}    --for=condition=Ready=True    --timeout=${WAIT_TIMEOUT}
    Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    Deployment Should Be Available    ${AUTH_NAMESPACE}    trino-${AUTH_XTRINODE}-coordinator    1
    Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    Deployment Should Be Available    ${AUTH_NAMESPACE}    trino-${AUTH_XTRINODE}-worker    1

Create Password Lifecycle Namespace If Missing
    ${result}=    Run Process    kubectl    create    namespace    ${AUTH_NAMESPACE}    stderr=STDOUT
    Log    ${result.stdout}
    IF    ${result.rc} != 0
        Should Contain    ${result.stdout}    AlreadyExists
    END

Apply Password Lifecycle Control Secret
    ${secret_yaml}=    Command Should Succeed    kubectl    create    secret    generic    ${AUTH_CONTROL_SECRET}    -n    ${AUTH_NAMESPACE}    --from-literal=password=${AUTH_CONTROL_PASSWORD}    --dry-run=client    -o    yaml
    ${secret_manifest}=    Set Variable    /tmp/xtrinode-password-lifecycle-control-secret.yaml
    Create File    ${secret_manifest}    ${secret_yaml}
    Command Should Succeed    kubectl    apply    -f    ${secret_manifest}

Apply Password Lifecycle XTrinode
    ${manifest}=    Set Variable    /tmp/xtrinode-password-lifecycle.yaml
    ${json}=    Set Variable    {"apiVersion":"analytics.xtrinode.io/v1","kind":"XTrinode","metadata":{"name":"${AUTH_XTRINODE}","namespace":"${AUTH_NAMESPACE}","labels":{"test.xtrinode.io/contract":"password-lifecycle-auth"}},"spec":{"size":"xs","minWorkers":1,"maxWorkers":1,"suspended":false,"autoSuspendAfter":"30m","trinoControlAuth":{"username":"${AUTH_CONTROL_USER}","passwordSecret":{"name":"${AUTH_CONTROL_SECRET}","key":"password"}},"routing":{"header":"X-Trino-XTrinode=${AUTH_XTRINODE}","routingGroup":"${AUTH_XTRINODE}"},"valuesOverlay":{"image":{"repository":"${TRINO_IMAGE_REPOSITORY}","tag":"${TRINO_IMAGE_TAG}","pullPolicy":"IfNotPresent"},"server":{"workers":1,"config":{"authenticationType":"PASSWORD"}},"auth":{"passwordAuth":"${AUTH_CONTROL_USER}:${AUTH_PASSWORD_HASH}"},"coordinator":{"resources":{"requests":{"cpu":"250m","memory":"1Gi"},"limits":{"cpu":"1","memory":"1536Mi"}},"startupProbe":{"exec":{"command":["/usr/lib/trino/bin/health-check"]},"initialDelaySeconds":10,"periodSeconds":5,"timeoutSeconds":3,"failureThreshold":90}},"worker":{"gracefulShutdown":{"enabled":true,"gracePeriodSeconds":5},"resources":{"requests":{"cpu":"250m","memory":"1Gi"},"limits":{"cpu":"1","memory":"1536Mi"}},"startupProbe":{"exec":{"command":["/usr/lib/trino/bin/health-check"]},"initialDelaySeconds":10,"periodSeconds":5,"timeoutSeconds":3,"failureThreshold":90}},"additionalConfigProperties":["internal-communication.shared-secret=${AUTH_INTERNAL_SECRET}","query.max-memory=512MB","query.max-memory-per-node=384MB","memory.heap-headroom-per-node=256MB"]}}}
    Create File    ${manifest}    ${json}
    Command Should Succeed    kubectl    apply    -f    ${manifest}

Start Password Lifecycle Coordinator Port Forward
    Stop Password Lifecycle Coordinator Port Forward
    Start Process    kubectl    port-forward    -n    ${AUTH_NAMESPACE}    svc/trino-${AUTH_XTRINODE}    ${AUTH_COORDINATOR_PORT}:8080    stdout=/tmp/xtrinode-password-lifecycle-port-forward.log    stderr=STDOUT    alias=xtrinode-password-lifecycle-port-forward
    Wait Until Keyword Succeeds    60s    1s    Direct Trino Query List Without Password Should Return    401

Stop Password Lifecycle Port Forwards
    Stop Password Lifecycle Coordinator Port Forward

Stop Password Lifecycle Coordinator Port Forward
    Run Keyword And Ignore Error    Terminate Process    xtrinode-password-lifecycle-port-forward    kill=True

Direct Trino Query List Without Password Should Return
    [Arguments]    ${expected_status}
    ${status}=    HTTP Request To File    GET    http://127.0.0.1:${AUTH_COORDINATOR_PORT}/v1/query    /tmp/xtrinode-password-lifecycle-query-unauth.json    ${EMPTY}    X-Forwarded-Proto: https
    Should Be Equal    ${status}    ${expected_status}

Direct Trino Query List With Control Password Should Return
    [Arguments]    ${expected_status}
    ${basic}=    Password Lifecycle Basic Auth Token
    ${status}=    HTTP Request To File    GET    http://127.0.0.1:${AUTH_COORDINATOR_PORT}/v1/query    /tmp/xtrinode-password-lifecycle-query-auth.json    ${EMPTY}    Authorization: Basic ${basic}    X-Trino-User: ${AUTH_CONTROL_USER}    X-Forwarded-Proto: https
    Should Be Equal    ${status}    ${expected_status}

Password Lifecycle Basic Auth Token
    ${raw}=    Set Variable    ${AUTH_CONTROL_USER}:${AUTH_CONTROL_PASSWORD}
    ${token}=    Evaluate    __import__("base64").b64encode($raw.encode()).decode()
    RETURN    ${token}

Worker Password Lifecycle Resources Should Be Wired
    ${worker}=    Kubectl Output    get    deployment    trino-${AUTH_XTRINODE}-worker    -n    ${AUTH_NAMESPACE}    -o    json
    ${worker_file}=    Set Variable    /tmp/xtrinode-password-lifecycle-worker.json
    Create File    ${worker_file}    ${worker}
    JQ Should Match    ${worker_file}    any(.spec.template.spec.containers[0].env[]?; .name == "XTRINODE_TRINO_CONTROL_USER" and .value == $user)    --arg    user    ${AUTH_CONTROL_USER}
    JQ Should Match    ${worker_file}    any(.spec.template.spec.containers[0].env[]?; .name == "XTRINODE_TRINO_CONTROL_PASSWORD" and .valueFrom.secretKeyRef.name == $secret and .valueFrom.secretKeyRef.key == "password")    --arg    secret    ${AUTH_CONTROL_SECRET}
    ${prestop_auth}=    Set Variable    -u "\${XTRINODE_TRINO_CONTROL_USER}:\${XTRINODE_TRINO_CONTROL_PASSWORD}"
    JQ Should Match    ${worker_file}    (.spec.template.spec.containers[0].lifecycle.preStop.exec.command | join(" ")) | contains($expected)    --arg    expected    ${prestop_auth}
    JQ Should Match    ${worker_file}    (.spec.template.spec.containers[0].lifecycle.preStop.exec.command | join(" ")) | contains("X-Forwarded-Proto: https")
    ${configmaps}=    Kubectl Output    get    configmap    -n    ${AUTH_NAMESPACE}    -l    app.kubernetes.io/instance=${AUTH_XTRINODE}    -o    json
    ${configmaps_file}=    Set Variable    /tmp/xtrinode-password-lifecycle-worker-configmaps.json
    Create File    ${configmaps_file}    ${configmaps}
    JQ Should Match    ${configmaps_file}    any(.items[]?; (.data["password-authenticator.properties"] // "") | contains("password-authenticator.name=file"))

Suspend Password Lifecycle XTrinode Through API
    ${status}=    HTTP Request To File    POST    http://127.0.0.1:${API_SERVER_PORT}/api/v1/runtimes/${AUTH_NAMESPACE}/${AUTH_XTRINODE}/suspend    /tmp/xtrinode-password-lifecycle-suspend.json    {}
    Should Be Equal    ${status}    202

Resume Password Lifecycle XTrinode Through API
    ${status}=    HTTP Request To File    POST    http://127.0.0.1:${API_SERVER_PORT}/api/v1/runtimes/${AUTH_NAMESPACE}/${AUTH_XTRINODE}/resume    /tmp/xtrinode-password-lifecycle-resume.json    {}    Content-Type: application/json
    Should Be Equal    ${status}    202

Password Lifecycle XTrinode Suspended State Should Be
    [Arguments]    ${expected}
    ${value}=    Kubectl Output    get    xtrinode    ${AUTH_XTRINODE}    -n    ${AUTH_NAMESPACE}    -o    jsonpath={.spec.suspended}
    ${value}=    Set Variable If    '${value}' == ''    false    ${value}
    Should Be Equal    ${value}    ${expected}

Cleanup Password Lifecycle Contract Objects
    ${valid_patch}=    Set Variable    {"spec":{"valuesOverlay":{"additionalConfigProperties":["internal-communication.shared-secret=${AUTH_INTERNAL_SECRET}","query.max-memory=512MB","query.max-memory-per-node=384MB","memory.heap-headroom-per-node=256MB"]}}}
    Run Command Allow Failure    kubectl    patch    xtrinode/${AUTH_XTRINODE}    -n    ${AUTH_NAMESPACE}    --type=merge    -p    ${valid_patch}
    Run Command Allow Failure    kubectl    delete    xtrinode/${AUTH_XTRINODE}    -n    ${AUTH_NAMESPACE}    --wait=false    --ignore-not-found=true
    Run Command Allow Failure    kubectl    wait    xtrinode/${AUTH_XTRINODE}    -n    ${AUTH_NAMESPACE}    --for=delete    --timeout=120s
    Run Command Allow Failure    kubectl    patch    xtrinode/${AUTH_XTRINODE}    -n    ${AUTH_NAMESPACE}    --type=merge    -p    {"metadata":{"finalizers":[]}}
    Run Command Allow Failure    kubectl    delete    xtrinode/${AUTH_XTRINODE}    -n    ${AUTH_NAMESPACE}    --wait=false    --ignore-not-found=true
    Run Command Allow Failure    kubectl    wait    xtrinode/${AUTH_XTRINODE}    -n    ${AUTH_NAMESPACE}    --for=delete    --timeout=60s
    Run Command Allow Failure    kubectl    delete    deployment,service,configmap,poddisruptionbudget,serviceaccount,horizontalpodautoscaler,scaledobject,triggerauthentication    -n    ${AUTH_NAMESPACE}    -l    app.kubernetes.io/instance=${AUTH_XTRINODE}    --ignore-not-found=true    --wait=false
    Run Command Allow Failure    kubectl    delete    secret    ${AUTH_CONTROL_SECRET}    trino-${AUTH_XTRINODE}-password-file    -n    ${AUTH_NAMESPACE}    --ignore-not-found=true

Dump Password Lifecycle Debug
    Dump Debug
    Run Command Allow Failure    kubectl    get    xtrinode/${AUTH_XTRINODE}    -n    ${AUTH_NAMESPACE}    -o    yaml
    Run Command Allow Failure    kubectl    get    deployment,service,configmap,secret,pods    -n    ${AUTH_NAMESPACE}    -l    app.kubernetes.io/instance=${AUTH_XTRINODE}    -o    yaml
    Run Command Allow Failure    kubectl    get    events    -n    ${AUTH_NAMESPACE}    --sort-by=.lastTimestamp
