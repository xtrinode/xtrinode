*** Settings ***
Documentation       Headless Locust load-test smoke against the local XTrinode gateway.
Resource            ../../e2e/robot/resources/local.resource
Suite Setup         Run Keywords    Ensure Local XTrinode Ready    AND    Start Local Port Forwards    AND    Wait For Gateway Backend Ready
Suite Teardown      Stop Local Port Forwards
Test Tags           local    k3d    loadtest    locust
Test Teardown       Run Keyword If Test Failed    Dump Debug

*** Variables ***
${LOCUSTFILE}              ${REPO_ROOT}/tilt/loadtest/locustfile.py
${UV}                      uv
${LOADTEST_USERS}          1
${LOADTEST_SPAWN_RATE}     1
${LOADTEST_RUN_TIME}       15s
${LOADTEST_WAIT_MIN}       0.5
${LOADTEST_WAIT_MAX}       1.0
${LOADTEST_MIN_REQUESTS}   2
${LOADTEST_QUERY}          SELECT count(*) FROM postgres.public.orders
${LOADTEST_AUTOSCALE_USERS}             2
${LOADTEST_AUTOSCALE_SPAWN_RATE}        1
${LOADTEST_AUTOSCALE_RUN_TIME}          300s
${LOADTEST_AUTOSCALE_WAIT_SECONDS}      420
${LOADTEST_AUTOSCALE_MAX_WORKERS}       2
${LOADTEST_AUTOSCALE_THRESHOLD}         0.5
${LOADTEST_AUTOSCALE_QUERY_TIMEOUT_SECONDS}    420
${LOADTEST_AUTOSCALE_QUERY}             SELECT count(*) FROM "local-tpch".sf1000.lineitem WHERE rand() >= 0

*** Test Cases ***
Locust Headless Load Runs Through Gateway
    [Tags]    loadtest-smoke
    ${smoke}=    Set Variable    /tmp/xtrinode-loadtest-smoke-query.json
    ${stats_prefix}=    Set Variable    /tmp/xtrinode-loadtest-stats
    ${stats_file}=    Set Variable    ${stats_prefix}.json
    Wait Until Keyword Succeeds    180s    2s    Gateway Statement Should Return Status    ${smoke}    ${LOADTEST_QUERY}    200
    Drain Trino Query    ${smoke}
    Remove File    ${stats_file}
    ${result}=    Run Process    ${UV}    run
    ...    --project
    ...    ${REPO_ROOT}/tilt/e2e
    ...    locust
    ...    -f
    ...    ${LOCUSTFILE}
    ...    --headless
    ...    --users
    ...    ${LOADTEST_USERS}
    ...    --spawn-rate
    ...    ${LOADTEST_SPAWN_RATE}
    ...    --run-time
    ...    ${LOADTEST_RUN_TIME}
    ...    --host
    ...    http://127.0.0.1:${GATEWAY_PORT}
    ...    --only-summary
    ...    --json-file
    ...    ${stats_prefix}
    ...    --exit-code-on-error
    ...    1
    ...    env:XTRINODE_ROUTE_HEADER=${XTRINODE_NAME}
    ...    env:XTRINODE_QUERY=${LOADTEST_QUERY}
    ...    env:XTRINODE_LOAD_WAIT_MIN=${LOADTEST_WAIT_MIN}
    ...    env:XTRINODE_LOAD_WAIT_MAX=${LOADTEST_WAIT_MAX}
    ...    stderr=STDOUT
    Log    ${result.stdout}
    Should Be Equal As Integers    ${result.rc}    0    msg=${result.stdout}
    File Should Exist    ${stats_file}
    JQ Should Match    ${stats_file}    (if type == "array" then . else (.stats // []) end) as $stats | (($stats | map(.num_requests // 0) | add) >= $min) and (($stats | map(.num_failures // 0) | add) == 0) and any($stats[]?; .name == "POST /v1/statement" and (.num_requests // 0) >= 1 and (.num_failures // 0) == 0)    --argjson    min    ${LOADTEST_MIN_REQUESTS}

Locust Load Drives Worker Autoscaling
    [Tags]    loadtest-autoscale    scaleout    keda
    Configure Query Autoscaling For Local XTrinode
    TRY
        Start Locust Autoscale Load
        Wait Until Keyword Succeeds    ${LOADTEST_AUTOSCALE_WAIT_SECONDS}s    5s    Deployment Spec Replicas Should Equal    ${NAMESPACE}    trino-${XTRINODE_NAME}-worker    ${LOADTEST_AUTOSCALE_MAX_WORKERS}
        Wait Until Keyword Succeeds    ${LOADTEST_AUTOSCALE_WAIT_SECONDS}s    5s    Deployment Should Be Available    ${NAMESPACE}    trino-${XTRINODE_NAME}-worker    ${LOADTEST_AUTOSCALE_MAX_WORKERS}
        Process Should Be Running    xtrinode-locust-autoscale
    FINALLY
        Run Keyword And Ignore Error    Terminate Process    xtrinode-locust-autoscale    kill=True
        Wait Until Keyword Succeeds    ${LOADTEST_AUTOSCALE_WAIT_SECONDS}s    5s    Deployment Spec Replicas Should Equal    ${NAMESPACE}    trino-${XTRINODE_NAME}-worker    1
        Restore Default Local XTrinode Autoscaling
    END

*** Keywords ***
Configure Query Autoscaling For Local XTrinode
    ${patch}=    Set Variable    {"spec":{"minWorkers":1,"maxWorkers":${LOADTEST_AUTOSCALE_MAX_WORKERS},"keda":{"enabled":true,"scalerType":"http","scalingMetric":"query","threshold":"${LOADTEST_AUTOSCALE_THRESHOLD}","scaleDownCooldown":"30s"},"valuesOverlay":{"server":{"workers":1}}}}
    Command Should Succeed    kubectl    patch    xtrinode/${XTRINODE_NAME}    -n    ${NAMESPACE}    --type=merge    -p    ${patch}
    Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    Command Should Succeed    kubectl    wait    xtrinode/${XTRINODE_NAME}    -n    ${NAMESPACE}    --for=condition=Ready=True    --timeout=30s
    Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    ScaledObject Max Replicas Should Equal    ${NAMESPACE}    trino-${XTRINODE_NAME}-workers    ${LOADTEST_AUTOSCALE_MAX_WORKERS}
    Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    Deployment Should Be Available    ${NAMESPACE}    trino-${XTRINODE_NAME}-coordinator    1
    Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    Deployment Should Be Available    ${NAMESPACE}    trino-${XTRINODE_NAME}-worker    1
    Wait For Gateway Backend Ready

Restore Default Local XTrinode Autoscaling
    ${patch}=    Set Variable    {"spec":{"minWorkers":1,"maxWorkers":1,"keda":{"enabled":true,"scalerType":"http","scalingMetric":"memory","threshold":"80","scaleDownCooldown":"30s"},"valuesOverlay":{"server":{"workers":1}}}}
    Command Should Succeed    kubectl    patch    xtrinode/${XTRINODE_NAME}    -n    ${NAMESPACE}    --type=merge    -p    ${patch}
    Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    ScaledObject Max Replicas Should Equal    ${NAMESPACE}    trino-${XTRINODE_NAME}-workers    1

Start Locust Autoscale Load
    Remove File    /tmp/xtrinode-locust-autoscale.log
    Start Process    ${UV}    run
    ...    --project
    ...    ${REPO_ROOT}/tilt/e2e
    ...    locust
    ...    -f
    ...    ${LOCUSTFILE}
    ...    --headless
    ...    --users
    ...    ${LOADTEST_AUTOSCALE_USERS}
    ...    --spawn-rate
    ...    ${LOADTEST_AUTOSCALE_SPAWN_RATE}
    ...    --run-time
    ...    ${LOADTEST_AUTOSCALE_RUN_TIME}
    ...    --host
    ...    http://127.0.0.1:${GATEWAY_PORT}
    ...    --only-summary
    ...    --exit-code-on-error
    ...    1
    ...    env:XTRINODE_ROUTE_HEADER=${XTRINODE_NAME}
    ...    env:XTRINODE_QUERY=${LOADTEST_AUTOSCALE_QUERY}
    ...    env:XTRINODE_QUERY_TIMEOUT_SECONDS=${LOADTEST_AUTOSCALE_QUERY_TIMEOUT_SECONDS}
    ...    env:XTRINODE_LOAD_WAIT_MIN=0.1
    ...    env:XTRINODE_LOAD_WAIT_MAX=0.2
    ...    stdout=/tmp/xtrinode-locust-autoscale.log
    ...    stderr=STDOUT
    ...    alias=xtrinode-locust-autoscale
    Wait Until Keyword Succeeds    60s    2s    Process Should Be Running    xtrinode-locust-autoscale
