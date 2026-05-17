*** Settings ***
Documentation       Process-level gateway contracts against in-cluster fake Trino backends.
Resource            resources/local.resource
Suite Setup         Setup Gateway Process Contract Suite
Suite Teardown      Teardown Gateway Process Contract Suite
Test Teardown       Run Keyword If Test Failed    Dump Debug
Test Tags           local    k3d    integration    gateway    process    redis

*** Variables ***
${PROCESS_BACKEND_SCRIPT}       ${REPO_ROOT}/tilt/e2e/fixtures/gateway-process-backend.py
${PROCESS_BACKEND_MANIFEST}     ${REPO_ROOT}/tilt/e2e/fixtures/gateway-process-backends.yaml
${BURST_HELPER}                 ${REPO_ROOT}/tilt/e2e/helpers/gateway-burst.sh
${ROUTES_PATCH_FILE}            /tmp/xtrinode-gateway-process-routes-patch.json
${ARGS_PATCH_FILE}              /tmp/xtrinode-gateway-process-args-patch.json
${RELOAD_ROUTE}                 e2e-process-reload
${STICKY_ROUTE}                 e2e-process-sticky
${CIRCUIT_ROUTE}                e2e-process-circuit
${HALF_OPEN_OVERLOAD_ROUTE}     e2e-process-half-open-overload
${HALF_OPEN_PAUSED_ROUTE}       e2e-process-half-open-paused
${RATE_ROUTE}                   e2e-process-rate-limit
${PROCESS_QUERY_ID}             20260508_000000_00001_e2e01
${PAUSED_FAIL_SEQUENCE}         500,500,500,500,500,200,200
${PAUSED_HEALTHY_SEQUENCE}      200,200
${GATEWAY_POD_A_PORT}           18082
${GATEWAY_POD_B_PORT}           18083

*** Test Cases ***
Gateway Reloads Routes From ConfigMap Without Restart
    Set Gateway Process Routes    ${RELOAD_ROUTE}    gateway-process-backend-a    process-a    RUNNING    true
    Wait Until Keyword Succeeds    60s    2s    Gateway Process Info Should Return Backend    ${RELOAD_ROUTE}    process-a
    ${pod_before}=    Gateway Pod UID

    Set Gateway Process Routes    ${RELOAD_ROUTE}    gateway-process-backend-b    process-b    RUNNING    true
    Wait Until Keyword Succeeds    60s    2s    Gateway Process Info Should Return Backend    ${RELOAD_ROUTE}    process-b
    ${pod_after}=    Gateway Pod UID
    Should Be Equal    ${pod_after}    ${pod_before}

Gateway Keeps Sticky Query On Draining Backend And Persists It In Redis
    Set Gateway Process Routes    ${STICKY_ROUTE}    gateway-process-backend-a    process-a    RUNNING    true
    Wait Until Keyword Succeeds    60s    2s    Gateway Process Info Should Return Backend    ${STICKY_ROUTE}    process-a

    ${status}=    HTTP Request To File    POST    http://127.0.0.1:${GATEWAY_PORT}/v1/statement    /tmp/xtrinode-gateway-process-sticky-start.json    SELECT 1    X-Trino-User: local-e2e-contracts    X-Trino-XTrinode: ${STICKY_ROUTE}
    Should Be Equal    ${status}    200
    JQ Should Match    /tmp/xtrinode-gateway-process-sticky-start.json    .id == $id and .backend == "process-a"    --arg    id    ${PROCESS_QUERY_ID}
    Redis Sticky Route Should Contain Process Backend    ${PROCESS_QUERY_ID}    gateway-process-backend-a

    Set Gateway Process Routes    ${STICKY_ROUTE}    gateway-process-backend-a    process-a    DRAINING    false
    Wait Until Keyword Succeeds    60s    2s    Gateway Sticky Continuation Should Return Backend    ${STICKY_ROUTE}    process-a
    Wait Until Keyword Succeeds    60s    2s    Gateway New Statement Should Return Status    ${STICKY_ROUTE}    503

Gateway Circuit Breaker Opens Against Failing Backend
    Set Gateway Process Routes    ${CIRCUIT_ROUTE}    gateway-process-backend-bad    process-bad    RUNNING    true
    Wait Until Keyword Succeeds    60s    2s    Gateway Process Route Should Match Status    ${CIRCUIT_ROUTE}    500|503
    FOR    ${index}    IN RANGE    6
        ${status}=    HTTP Request To File    GET    http://127.0.0.1:${GATEWAY_PORT}/v1/info    /tmp/xtrinode-gateway-process-circuit-${index}.json    ${EMPTY}    X-Trino-User: local-e2e-contracts    X-Trino-XTrinode: ${CIRCUIT_ROUTE}
        Should Match Regexp    ${status}    500|503
    END
    Wait Until Keyword Succeeds    60s    1s    Gateway Process Route Should Return Status    ${CIRCUIT_ROUTE}    503

Gateway Circuit Breaker Allows One Concurrent Half Open Overload Probe
    Restart Gateway Process Backend    gateway-process-backend-flaky-overload
    Set Gateway Process Routes    ${HALF_OPEN_OVERLOAD_ROUTE}    gateway-process-backend-flaky-overload    process-flaky-overload    RUNNING    true
    Wait Until Keyword Succeeds    60s    2s    Gateway Process Route Should Return Status    ${HALF_OPEN_OVERLOAD_ROUTE}    500
    FOR    ${index}    IN RANGE    4
        Gateway Process Route Should Return Status    ${HALF_OPEN_OVERLOAD_ROUTE}    500
    END
    Gateway Process Route Should Return Status    ${HALF_OPEN_OVERLOAD_ROUTE}    503
    Sleep    31s
    ${summary}=    Run Gateway Burst    ${HALF_OPEN_OVERLOAD_ROUTE}    process-flaky-overload    6    /tmp/xtrinode-gateway-half-open-overload
    Should Contain    ${summary}    total=6
    Should Contain    ${summary}    status_503=6
    Should Contain    ${summary}    backend_body_count=1
    Gateway Process Route Should Return Status    ${HALF_OPEN_OVERLOAD_ROUTE}    503

Gateway Circuit Breaker Recovers After Half Open Paused Probe
    TRY
        Set Gateway Process Backend Status Sequence    gateway-process-backend-flaky-paused    ${PAUSED_FAIL_SEQUENCE}
        Restart Gateway Process Backend    gateway-process-backend-flaky-paused
        Set Gateway Process Routes    ${HALF_OPEN_PAUSED_ROUTE}    gateway-process-backend-flaky-paused    process-flaky-paused    RUNNING    true
        Wait Until Keyword Succeeds    60s    2s    Gateway Process Route Should Return Status    ${HALF_OPEN_PAUSED_ROUTE}    500
        FOR    ${index}    IN RANGE    4
            Gateway Process Route Should Return Status    ${HALF_OPEN_PAUSED_ROUTE}    500
        END
        Gateway Process Route Should Return Status    ${HALF_OPEN_PAUSED_ROUTE}    503
        Command Should Succeed    kubectl    scale    deployment/gateway-process-backend-flaky-paused    -n    ${NAMESPACE}    --replicas=0
        Wait Until Keyword Succeeds    120s    2s    Backend Pods Should Be Gone    gateway-process-backend-flaky-paused
        Sleep    31s
        ${summary}=    Run Gateway Burst    ${HALF_OPEN_PAUSED_ROUTE}    process-flaky-paused    4    /tmp/xtrinode-gateway-half-open-paused
        Should Contain    ${summary}    total=4
        Should Contain    ${summary}    status_503=4
        Should Contain    ${summary}    backend_body_count=0
        Set Gateway Process Backend Status Sequence    gateway-process-backend-flaky-paused    ${PAUSED_HEALTHY_SEQUENCE}
        Command Should Succeed    kubectl    scale    deployment/gateway-process-backend-flaky-paused    -n    ${NAMESPACE}    --replicas=1
        Command Should Succeed    kubectl    rollout    status    deployment/gateway-process-backend-flaky-paused    -n    ${NAMESPACE}    --timeout=180s
        Wait Until Keyword Succeeds    180s    3s    Deployment Should Be Available    ${NAMESPACE}    gateway-process-backend-flaky-paused    1
        Sleep    31s
        Wait Until Keyword Succeeds    60s    2s    Gateway Process Info Should Return Backend    ${HALF_OPEN_PAUSED_ROUTE}    process-flaky-paused
    FINALLY
        Run Command Allow Failure    kubectl    set    env    deployment/gateway-process-backend-flaky-paused    -n    ${NAMESPACE}    STATUS_SEQUENCE=${PAUSED_FAIL_SEQUENCE}
        Run Command Allow Failure    kubectl    scale    deployment/gateway-process-backend-flaky-paused    -n    ${NAMESPACE}    --replicas=1
        Wait Until Keyword Succeeds    180s    3s    Deployment Should Be Available    ${NAMESPACE}    gateway-process-backend-flaky-paused    1
    END

Gateway Rate Limit Ignores Spoofed Forwarded For Headers
    TRY
        Patch Gateway Rate Limit Args    3    1h
        Clear Gateway Rate Limit State
        Set Gateway Process Routes    ${RATE_ROUTE}    gateway-process-backend-b    process-b    RUNNING    true
        Wait Until Keyword Succeeds    60s    2s    Gateway Process Info Should Return Backend    ${RATE_ROUTE}    process-b
        Clear Gateway Rate Limit State
        ${status1}=    Gateway Process Info With Forwarded For Should Return Status    ${RATE_ROUTE}    10.0.0.1
        ${status2}=    Gateway Process Info With Forwarded For Should Return Status    ${RATE_ROUTE}    10.0.0.2
        ${status3}=    Gateway Process Info With Forwarded For Should Return Status    ${RATE_ROUTE}    10.0.0.3
        ${status4}=    Gateway Process Info With Forwarded For Should Return Status    ${RATE_ROUTE}    10.0.0.4
        Should Be Equal    ${status1}    200
        Should Be Equal    ${status2}    200
        Should Be Equal    ${status3}    200
        Should Be Equal    ${status4}    429
    FINALLY
        Restore Gateway Args
    END

Gateway Redis Rate Limit Token Bucket Refills One Token Per Interval
    TRY
        Patch Gateway Rate Limit Args    1    4s
        Clear Gateway Rate Limit State
        Set Gateway Process Routes    ${RATE_ROUTE}    gateway-process-backend-b    process-b    RUNNING    true
        Wait Until Keyword Succeeds    60s    2s    Gateway Process Info Should Return Backend    ${RATE_ROUTE}    process-b
        Clear Gateway Rate Limit State
        Gateway Process Route Should Return Status    ${RATE_ROUTE}    200
        Gateway Process Route Should Return Status    ${RATE_ROUTE}    429
        Sleep    1s
        Gateway Process Route Should Return Status    ${RATE_ROUTE}    429
        Sleep    4s
        Gateway Process Route Should Return Status    ${RATE_ROUTE}    200
    FINALLY
        Restore Gateway Args
    END

Gateway Redis Rate Limit Is Shared Across Gateway Replicas
    TRY
        Patch Gateway Rate Limit Args    3    1h
        Scale Deployment And Wait Available    ${GATEWAY_NAMESPACE}    ${GATEWAY_SERVICE}    2
        Start Gateway Pod Port Forwards
        Clear Gateway Rate Limit State
        Set Gateway Process Routes    ${RATE_ROUTE}    gateway-process-backend-b    process-b    RUNNING    true
        Wait Until Keyword Succeeds    60s    2s    Gateway Process Info Should Return Backend    ${RATE_ROUTE}    process-b
        Clear Gateway Rate Limit State
        Gateway Process Info Via Port Should Return Status    ${GATEWAY_POD_A_PORT}    ${RATE_ROUTE}    200
        Gateway Process Info Via Port Should Return Status    ${GATEWAY_POD_B_PORT}    ${RATE_ROUTE}    200
        Gateway Process Info Via Port Should Return Status    ${GATEWAY_POD_A_PORT}    ${RATE_ROUTE}    200
        Gateway Process Info Via Port Should Return Status    ${GATEWAY_POD_B_PORT}    ${RATE_ROUTE}    429
    FINALLY
        Stop Gateway Pod Port Forwards
        Scale Deployment And Wait Available    ${GATEWAY_NAMESPACE}    ${GATEWAY_SERVICE}    1
        Restore Gateway Args
    END

*** Keywords ***
Setup Gateway Process Contract Suite
    Create Local Namespace
    Save Original Gateway Routes
    Save Original Gateway Args
    Deploy Gateway Process Backends
    Start Local Port Forwards

Teardown Gateway Process Contract Suite
    Restore Original Gateway Routes
    Restore Gateway Args
    Stop Gateway Pod Port Forwards
    Stop Local Port Forwards
    Run Command Allow Failure    kubectl    delete    deployment    -n    ${NAMESPACE}    gateway-process-backend-a    gateway-process-backend-b    gateway-process-backend-bad    gateway-process-backend-flaky-overload    gateway-process-backend-flaky-paused    --ignore-not-found=true
    Run Command Allow Failure    kubectl    delete    service    -n    ${NAMESPACE}    gateway-process-backend-a    gateway-process-backend-b    gateway-process-backend-bad    gateway-process-backend-flaky-overload    gateway-process-backend-flaky-paused    --ignore-not-found=true
    Run Command Allow Failure    kubectl    delete    configmap    -n    ${NAMESPACE}    gateway-process-backend-script    --ignore-not-found=true

Save Original Gateway Routes
    ${routes}=    Kubectl Output    get    configmap    trino-gateway-routes    -n    ${GATEWAY_NAMESPACE}    -o    ${GATEWAY_ROUTES_OUTPUT}
    Set Suite Variable    ${ORIGINAL_GATEWAY_ROUTES}    ${routes}

Save Original Gateway Args
    ${args}=    Kubectl Output    get    deployment    ${GATEWAY_SERVICE}    -n    ${GATEWAY_NAMESPACE}    -o    jsonpath={.spec.template.spec.containers[0].args}
    Set Suite Variable    ${ORIGINAL_GATEWAY_ARGS}    ${args}

Restore Original Gateway Routes
    Run Keyword And Ignore Error    Patch Gateway Routes ConfigMap    ${ORIGINAL_GATEWAY_ROUTES}

Restore Gateway Args
    Run Keyword And Ignore Error    Patch Gateway Args From JSON    ${ORIGINAL_GATEWAY_ARGS}
    Run Keyword And Ignore Error    Command Should Succeed    kubectl    rollout    status    deployment/${GATEWAY_SERVICE}    -n    ${GATEWAY_NAMESPACE}    --timeout=180s
    Start Local Port Forwards

Deploy Gateway Process Backends
    ${configmap_yaml}=    Command Should Succeed    kubectl    create    configmap    gateway-process-backend-script    -n    ${NAMESPACE}    --from-file=server.py=${PROCESS_BACKEND_SCRIPT}    --dry-run=client    -o    yaml
    Create File    /tmp/xtrinode-gateway-process-backend-cm.yaml    ${configmap_yaml}
    Command Should Succeed    kubectl    apply    -f    /tmp/xtrinode-gateway-process-backend-cm.yaml
    Command Should Succeed    kubectl    apply    -n    ${NAMESPACE}    -f    ${PROCESS_BACKEND_MANIFEST}
    Restart Gateway Process Backend Deployments
    Wait Until Keyword Succeeds    180s    3s    Deployment Should Be Available    ${NAMESPACE}    gateway-process-backend-a    1
    Wait Until Keyword Succeeds    180s    3s    Deployment Should Be Available    ${NAMESPACE}    gateway-process-backend-b    1
    Wait Until Keyword Succeeds    180s    3s    Deployment Should Be Available    ${NAMESPACE}    gateway-process-backend-bad    1
    Wait Until Keyword Succeeds    180s    3s    Deployment Should Be Available    ${NAMESPACE}    gateway-process-backend-flaky-overload    1
    Wait Until Keyword Succeeds    180s    3s    Deployment Should Be Available    ${NAMESPACE}    gateway-process-backend-flaky-paused    1

Restart Gateway Process Backend Deployments
    Restart Gateway Process Backend    gateway-process-backend-a
    Restart Gateway Process Backend    gateway-process-backend-b
    Restart Gateway Process Backend    gateway-process-backend-bad
    Restart Gateway Process Backend    gateway-process-backend-flaky-overload
    Restart Gateway Process Backend    gateway-process-backend-flaky-paused

Restart Gateway Process Backend
    [Arguments]    ${deployment}
    Command Should Succeed    kubectl    rollout    restart    deployment/${deployment}    -n    ${NAMESPACE}
    Command Should Succeed    kubectl    rollout    status    deployment/${deployment}    -n    ${NAMESPACE}    --timeout=180s

Set Gateway Process Backend Status Sequence
    [Arguments]    ${deployment}    ${sequence}
    Command Should Succeed    kubectl    set    env    deployment/${deployment}    -n    ${NAMESPACE}    STATUS_SEQUENCE=${sequence}

Backend Pods Should Be Gone
    [Arguments]    ${app}
    ${pods}=    Kubectl Output    get    pods    -n    ${NAMESPACE}    -l    app=${app}    -o    jsonpath={.items[*].metadata.name}
    Should Be Empty    ${pods}

Set Gateway Process Routes
    [Arguments]    ${route_name}    ${backend_service}    ${backend_name}    ${state}    ${active}
    ${routes}=    Set Variable    {"routes":[{"name":"${route_name}","routingGroup":"${route_name}","header":"${route_name}","backends":[{"name":"${backend_service}","namespace":"${NAMESPACE}","coordinatorURL":"http://${backend_service}.${NAMESPACE}.svc.cluster.local:8080","state":"${state}","active":${active},"capacityUnits":1}]}]}
    Patch Gateway Routes ConfigMap    ${routes}

Patch Gateway Routes ConfigMap
    [Arguments]    ${routes}
    ${patch}=    Run Process    jq    -n    --arg    routes    ${routes}    {"data":{"routes.yaml":$routes}}    stdout=${ROUTES_PATCH_FILE}    stderr=STDOUT
    Should Be Equal As Integers    ${patch.rc}    0    msg=${patch.stdout}
    Command Should Succeed    kubectl    patch    configmap    trino-gateway-routes    -n    ${GATEWAY_NAMESPACE}    --type=merge    --patch-file    ${ROUTES_PATCH_FILE}

Gateway Process Info Should Return Backend
    [Arguments]    ${route_name}    ${backend}
    ${status}=    HTTP Request To File    GET    http://127.0.0.1:${GATEWAY_PORT}/v1/info    /tmp/xtrinode-gateway-process-info-${route_name}.json    ${EMPTY}    X-Trino-User: local-e2e-contracts    X-Trino-XTrinode: ${route_name}
    Should Be Equal    ${status}    200
    JQ Should Match    /tmp/xtrinode-gateway-process-info-${route_name}.json    .backend == $backend    --arg    backend    ${backend}

Gateway Process Route Should Return Status
    [Arguments]    ${route_name}    ${expected_status}
    ${status}=    HTTP Request With Headers To File    GET    http://127.0.0.1:${GATEWAY_PORT}/v1/info    /tmp/xtrinode-gateway-process-status-${route_name}.json    /tmp/xtrinode-gateway-process-status-${route_name}.headers    ${EMPTY}    X-Trino-User: local-e2e-contracts    X-Trino-XTrinode: ${route_name}
    Should Be Equal    ${status}    ${expected_status}

Gateway Process Route Should Match Status
    [Arguments]    ${route_name}    ${expected_status_regex}
    ${status}=    HTTP Request With Headers To File    GET    http://127.0.0.1:${GATEWAY_PORT}/v1/info    /tmp/xtrinode-gateway-process-status-${route_name}.json    /tmp/xtrinode-gateway-process-status-${route_name}.headers    ${EMPTY}    X-Trino-User: local-e2e-contracts    X-Trino-XTrinode: ${route_name}
    Should Match Regexp    ${status}    ${expected_status_regex}

Gateway New Statement Should Return Status
    [Arguments]    ${route_name}    ${expected_status}
    ${status}=    HTTP Request To File    POST    http://127.0.0.1:${GATEWAY_PORT}/v1/statement    /tmp/xtrinode-gateway-process-sticky-new.json    SELECT 1    X-Trino-User: local-e2e-contracts    X-Trino-XTrinode: ${route_name}
    Should Be Equal    ${status}    ${expected_status}

Gateway Sticky Continuation Should Return Backend
    [Arguments]    ${route_name}    ${backend}
    ${status}=    HTTP Request To File    GET    http://127.0.0.1:${GATEWAY_PORT}/v1/statement/queued/${PROCESS_QUERY_ID}/1    /tmp/xtrinode-gateway-process-sticky-continuation.json    ${EMPTY}    X-Trino-User: local-e2e-contracts    X-Trino-XTrinode: ${route_name}
    Should Be Equal    ${status}    200
    JQ Should Match    /tmp/xtrinode-gateway-process-sticky-continuation.json    .backend == $backend    --arg    backend    ${backend}

Gateway Process Info With Forwarded For Should Return Status
    [Arguments]    ${route_name}    ${forwarded_for}
    ${status}=    HTTP Request To File    GET    http://127.0.0.1:${GATEWAY_PORT}/v1/info    /tmp/xtrinode-gateway-process-rate-${forwarded_for}.json    ${EMPTY}    X-Trino-User: local-e2e-contracts    X-Trino-XTrinode: ${route_name}    X-Forwarded-For: ${forwarded_for}
    RETURN    ${status}

Gateway Process Info Via Port Should Return Status
    [Arguments]    ${port}    ${route_name}    ${expected_status}
    ${status}=    HTTP Request To File    GET    http://127.0.0.1:${port}/v1/info    /tmp/xtrinode-gateway-process-port-${port}-${route_name}.json    ${EMPTY}    X-Trino-User: local-e2e-contracts    X-Trino-XTrinode: ${route_name}
    Should Be Equal    ${status}    ${expected_status}
    RETURN    ${status}

Run Gateway Burst
    [Arguments]    ${route_name}    ${expected_backend}    ${requests}    ${output_dir}    ${port}=${GATEWAY_PORT}
    ${url}=    Set Variable    http://127.0.0.1:${port}/v1/info
    ${summary}=    Command Should Succeed    bash    ${BURST_HELPER}    ${url}    ${route_name}    ${requests}    ${expected_backend}    ${output_dir}
    RETURN    ${summary}

Redis Sticky Route Should Contain Process Backend
    [Arguments]    ${query_id}    ${backend_name}
    ${sticky}=    Kubectl Output    exec    -n    ${GATEWAY_NAMESPACE}    deployment/xtrinode-gateway-redis    --    redis-cli    GET    query:${query_id}
    Should Contain    ${sticky}    ${NAMESPACE}
    Should Contain    ${sticky}    ${backend_name}
    Should Contain    ${sticky}    ${backend_name}.${NAMESPACE}.svc.cluster.local:8080

Gateway Pod UID
    ${uid}=    Kubectl Output    get    pod    -n    ${GATEWAY_NAMESPACE}    -l    app.kubernetes.io/name=xtrinode-gateway,app.kubernetes.io/component=gateway    -o    jsonpath={.items[0].metadata.uid}
    RETURN    ${uid}

Start Gateway Pod Port Forwards
    Stop Gateway Pod Port Forwards
    ${pods}=    Kubectl Output    get    pods    -n    ${GATEWAY_NAMESPACE}    -l    app.kubernetes.io/name=xtrinode-gateway,app.kubernetes.io/component=gateway    --field-selector=status.phase=Running    -o    name
    @{pod_lines}=    Split To Lines    ${pods}
    ${pod_count}=    Get Length    ${pod_lines}
    Should Be True    ${pod_count} >= 2
    ${pod_a}=    Remove String    ${pod_lines}[0]    pod/
    ${pod_b}=    Remove String    ${pod_lines}[1]    pod/
    Start Process    kubectl    port-forward    -n    ${GATEWAY_NAMESPACE}    pod/${pod_a}    ${GATEWAY_POD_A_PORT}:8080    stdout=/tmp/xtrinode-robot-gateway-pod-a-port-forward.log    stderr=STDOUT    alias=xtrinode-gateway-pod-a-port-forward
    Start Process    kubectl    port-forward    -n    ${GATEWAY_NAMESPACE}    pod/${pod_b}    ${GATEWAY_POD_B_PORT}:8080    stdout=/tmp/xtrinode-robot-gateway-pod-b-port-forward.log    stderr=STDOUT    alias=xtrinode-gateway-pod-b-port-forward
    Wait Until Keyword Succeeds    45s    1s    HTTP Should Succeed    http://127.0.0.1:${GATEWAY_POD_A_PORT}/health
    Wait Until Keyword Succeeds    45s    1s    HTTP Should Succeed    http://127.0.0.1:${GATEWAY_POD_B_PORT}/health

Stop Gateway Pod Port Forwards
    Run Keyword And Ignore Error    Terminate Process    xtrinode-gateway-pod-a-port-forward    kill=True
    Run Keyword And Ignore Error    Terminate Process    xtrinode-gateway-pod-b-port-forward    kill=True

Patch Gateway Rate Limit Args
    [Arguments]    ${capacity}    ${refill_rate}
    Create File    /tmp/xtrinode-gateway-original-args.json    ${ORIGINAL_GATEWAY_ARGS}
    Create File    /tmp/xtrinode-gateway-rate-filter.jq    map(if startswith("--rate-limit-capacity=") then "--rate-limit-capacity=" + $capacity elif startswith("--rate-limit-refill-rate=") then "--rate-limit-refill-rate=" + $refill else . end)
    ${patched_args}=    Run Process    jq    -c    --arg    capacity    ${capacity}    --arg    refill    ${refill_rate}    -f    /tmp/xtrinode-gateway-rate-filter.jq    /tmp/xtrinode-gateway-original-args.json    stderr=STDOUT
    Should Be Equal As Integers    ${patched_args.rc}    0    msg=${patched_args.stdout}
    Patch Gateway Args From JSON    ${patched_args.stdout}
    Command Should Succeed    kubectl    rollout    status    deployment/${GATEWAY_SERVICE}    -n    ${GATEWAY_NAMESPACE}    --timeout=180s
    Start Local Port Forwards

Clear Gateway Rate Limit State
    Run Command Allow Failure    kubectl    exec    -n    ${GATEWAY_NAMESPACE}    deployment/xtrinode-gateway-redis    --    redis-cli    EVAL    for _,k in ipairs(redis.call('keys','rl:*')) do redis.call('del',k) end return 1    0

Patch Gateway Args From JSON
    [Arguments]    ${args_json}
    Create File    /tmp/xtrinode-gateway-args-to-patch.json    ${args_json}
    ${patch}=    Run Process    jq    -n    --slurpfile    args    /tmp/xtrinode-gateway-args-to-patch.json    [{"op":"replace","path":"/spec/template/spec/containers/0/args","value":$args[0]}]    stdout=${ARGS_PATCH_FILE}    stderr=STDOUT
    Should Be Equal As Integers    ${patch.rc}    0    msg=${patch.stdout}
    Command Should Succeed    kubectl    patch    deployment    ${GATEWAY_SERVICE}    -n    ${GATEWAY_NAMESPACE}    --type=json    --patch-file    ${ARGS_PATCH_FILE}
