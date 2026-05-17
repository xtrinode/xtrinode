*** Settings ***
Documentation       API server and gateway HTTP contract tests against the local real-Trino deployment.
Resource            resources/local.resource
Suite Setup         Run Keywords    Ensure Local XTrinode Ready    AND    Start Local Port Forwards    AND    Wait For Gateway Backend Ready
Suite Teardown      Stop Local Port Forwards
Test Tags           local    k3d    contracts    api    gateway
Test Teardown       Run Keyword If Test Failed    Dump Debug

*** Test Cases ***
API Server Requires Bearer Token
    ${body}=    Set Variable    /tmp/xtrinode-api-noauth.json
    ${result}=    Run Process    curl    -sS    -o    ${body}    -w    ${CURL_HTTP_CODE_FORMAT}    http://127.0.0.1:${API_SERVER_PORT}/api/v1/runtimes    stderr=STDOUT
    Log    ${result.stdout}
    Should Be Equal As Integers    ${result.rc}    0    msg=${result.stdout}
    ${status}=    Strip String    ${result.stdout}
    Should Be Equal    ${status}    401
    JQ Should Match    ${body}    .code == "UNAUTHORIZED"

API Status Contract Is Stable
    ${body}=    Set Variable    /tmp/xtrinode-api-status.json
    ${status}=    HTTP Request To File    GET    http://127.0.0.1:${API_SERVER_PORT}/api/v1/runtimes/${NAMESPACE}/${XTRINODE_NAME}/status    ${body}
    Should Be Equal    ${status}    200
    ${coordinator}=    Set Variable    http://trino-${XTRINODE_NAME}.${NAMESPACE}.svc.cluster.local:8080
    JQ Should Match    ${body}    .phase == "Ready" and .coordinatorURL == $coordinator and .workers == 1 and (.conditions | type == "array")    --arg    coordinator    ${coordinator}

API Runtime Object Contract Is Stable
    ${body}=    Set Variable    /tmp/xtrinode-api-runtime.json
    ${status}=    HTTP Request To File    GET    http://127.0.0.1:${API_SERVER_PORT}/api/v1/runtimes/${NAMESPACE}/${XTRINODE_NAME}    ${body}
    Should Be Equal    ${status}    200
    JQ Should Match    ${body}    .apiVersion == "analytics.xtrinode.io/v1" and .kind == "XTrinode" and .metadata.namespace == $namespace and .metadata.name == $name and .spec.size == "xs" and .spec.routing.routingGroup == $name and .spec.valuesOverlay.image.tag == $tag and .status.phase == "Ready"    --arg    namespace    ${NAMESPACE}    --arg    name    ${XTRINODE_NAME}    --arg    tag    ${TRINO_IMAGE_TAG}

API Runtime List Includes Local XTrinode
    ${body}=    Set Variable    /tmp/xtrinode-api-runtimes.json
    ${status}=    HTTP Request To File    GET    http://127.0.0.1:${API_SERVER_PORT}/api/v1/runtimes    ${body}
    Should Be Equal    ${status}    200
    JQ Should Match    ${body}    any(.[]; .metadata.namespace == $namespace and .metadata.name == $name)    --arg    namespace    ${NAMESPACE}    --arg    name    ${XTRINODE_NAME}

API Error Contracts Are Explicit
    API Error Contract Should Hold    GET     /api/v1/runtimes/${NAMESPACE}/missing-local/status              404    NOT_FOUND             missing runtime
    API Error Contract Should Hold    POST    /api/v1/runtimes/${NAMESPACE}/${XTRINODE_NAME}/status           405    METHOD_NOT_ALLOWED    status method guard
    API Error Contract Should Hold    GET     /api/v1/runtimes/${NAMESPACE}/${XTRINODE_NAME}/resume           405    METHOD_NOT_ALLOWED    resume method guard
    API Error Contract Should Hold    GET     /api/v1/runtimes/${NAMESPACE}/${XTRINODE_NAME}/suspend          405    METHOD_NOT_ALLOWED    suspend method guard
    API Error Contract Should Hold    GET     /api/v1/runtimes/only-one-part                                  400    INVALID_PATH          invalid path guard
    API Error Contract Should Hold    GET     /api/v1/runtimes/Bad_NS/${XTRINODE_NAME}/status                 400    INVALID_NAMESPACE     invalid namespace guard
    API Error Contract Should Hold    GET     /api/v1/runtimes/${NAMESPACE}/Bad_Name/status                   400    INVALID_NAME          invalid name guard
    API Error Contract Should Hold    GET     /api/v1/runtimes/${NAMESPACE}/${XTRINODE_NAME}/unknown          400    UNKNOWN_ACTION        unknown action guard
    API Error Contract Should Hold    POST    /api/v1/runtimes                                                400    INVALID_REQUEST       create unknown field guard    {"name":"bad-local","namespace":"team-local","size":"xs","unexpected":true}
    API Error Contract Should Hold    POST    /api/v1/runtimes                                                400    MISSING_NAME          create missing name guard     {"namespace":"team-local","size":"xs"}
    API Error Contract Should Hold    POST    /api/v1/runtimes                                                400    INVALID_SIZE          create invalid size guard     {"name":"bad-local","namespace":"team-local","size":"xxl"}
    API Error Contract Should Hold    POST    /api/v1/resume                                                    400    INVALID_REQUEST       unified resume empty body      {}
    API Error Contract Should Hold    POST    /api/v1/resume                                                    400    INVALID_REQUEST       unified resume body guard     {"routingGroup":"local-trino-keda","unexpected":true}
    API Error Contract Should Hold    POST    /api/v1/resume                                                    400    INVALID_NAMESPACE     unified resume namespace guard    {"candidate":{"namespace":"Bad_NS","name":"local-trino-keda"}}
    API Error Contract Should Hold    POST    /api/v1/resume                                                    400    INVALID_NAME          unified resume name guard     {"candidate":{"namespace":"team-local","name":"Bad_Name"}}
    API Error Contract Should Hold    POST    /api/v1/resume                                                    404    NO_CANDIDATE          unified resume missing routing group    {"routingGroup":"missing-local-route"}

Gateway Returns Expected Route Status Codes
    ${body}=    Set Variable    /tmp/xtrinode-gateway-info.json
    ${status}=    HTTP Request To File    GET    http://127.0.0.1:${GATEWAY_PORT}/v1/info    ${body}    ${EMPTY}    X-Trino-User: local-e2e-contracts    X-Trino-XTrinode: ${XTRINODE_NAME}
    Should Be Equal    ${status}    200
    ${missing}=    Set Variable    /tmp/xtrinode-gateway-missing-route.txt
    ${missing_status}=    HTTP Request To File    GET    http://127.0.0.1:${GATEWAY_PORT}/v1/info    ${missing}    ${EMPTY}    X-Trino-User: local-e2e-contracts    X-Trino-XTrinode: missing-local
    Should Be Equal    ${missing_status}    404
    ${missing_body}=    Get File    ${missing}
    Should Contain    ${missing_body}    No route found

Gateway Proxies Real Trino Statement Contract
    ${body}=    Set Variable    /tmp/xtrinode-gateway-statement.json
    ${status}=    HTTP Request To File    POST    http://127.0.0.1:${GATEWAY_PORT}/v1/statement    ${body}    SELECT 1    X-Trino-User: local-e2e-contracts    X-Trino-XTrinode: ${XTRINODE_NAME}
    Should Be Equal    ${status}    200
    JQ Should Match    ${body}    (.id | test("^[0-9]{8}_[0-9]{6}_[0-9]{5}_[a-z0-9]+$")) and (.infoUri | type == "string") and (.stats.state | type == "string")
    Drain Trino Query    ${body}

Gateway Stores Sticky Query Route In Redis
    ${body}=    Set Variable    /tmp/xtrinode-gateway-sticky-redis.json
    Gateway Statement Should Return Status    ${body}    SELECT 1    200
    ${query_id}=    Command Should Succeed    jq    -r    .id    ${body}
    Redis Sticky Route Should Contain Local Backend    ${query_id}
    Drain Trino Query    ${body}

Gateway Redis Sticky Route Survives Pod Loss
    TRY
        Scale Deployment And Wait Available    ${GATEWAY_NAMESPACE}    xtrinode-gateway    2
        Start Local Port Forwards
        ${body}=    Set Variable    /tmp/xtrinode-gateway-sticky-pod-loss-statement.json
        Gateway Statement Should Return Status    ${body}    SELECT 1    200
        ${query_id}=    Command Should Succeed    jq    -r    .id    ${body}
        Redis Sticky Route Should Contain Local Backend    ${query_id}
        ${pod}=    Kubectl Output    get    pods    -n    ${GATEWAY_NAMESPACE}    -l    app.kubernetes.io/component=gateway    -o    jsonpath={.items[0].metadata.name}
        Command Should Succeed    kubectl    delete    pod    ${pod}    -n    ${GATEWAY_NAMESPACE}    --wait=false
        Start Local Port Forwards
        Drain Trino Query    ${body}
        Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    Deployment Should Be Available    ${GATEWAY_NAMESPACE}    xtrinode-gateway    2
    FINALLY
        Scale Deployment And Wait Available    ${GATEWAY_NAMESPACE}    xtrinode-gateway    1
        Start Local Port Forwards
    END

Gateway Redis Sticky Route Survives Gateway Rollout
    ${body}=    Set Variable    /tmp/xtrinode-gateway-sticky-rollout-statement.json
    Gateway Statement Should Return Status    ${body}    SELECT 1    200
    ${query_id}=    Command Should Succeed    jq    -r    .id    ${body}
    Redis Sticky Route Should Contain Local Backend    ${query_id}
    Command Should Succeed    kubectl    rollout    restart    deployment/xtrinode-gateway    -n    ${GATEWAY_NAMESPACE}
    Command Should Succeed    kubectl    rollout    status    deployment/xtrinode-gateway    -n    ${GATEWAY_NAMESPACE}    --timeout=180s
    Start Local Port Forwards
    Drain Trino Query    ${body}

Gateway Falls Back Locally When Redis Is Unavailable
    TRY
        Command Should Succeed    kubectl    scale    deployment/xtrinode-gateway-redis    -n    ${GATEWAY_NAMESPACE}    --replicas=0
        Wait Until Keyword Succeeds    120s    2s    Deployment Available Replicas Should Equal    ${GATEWAY_NAMESPACE}    xtrinode-gateway-redis    0
        Start Local Port Forwards
        ${info}=    Set Variable    /tmp/xtrinode-gateway-redis-outage-info.json
        Wait Until Keyword Succeeds    90s    2s    Gateway Info Should Return Status    ${info}    200
        ${statement}=    Set Variable    /tmp/xtrinode-gateway-redis-outage-statement.json
        Gateway Statement Should Return Status    ${statement}    SELECT 1    200
        Drain Trino Query    ${statement}
    FINALLY
        Command Should Succeed    kubectl    scale    deployment/xtrinode-gateway-redis    -n    ${GATEWAY_NAMESPACE}    --replicas=1
        Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    Deployment Should Be Available    ${GATEWAY_NAMESPACE}    xtrinode-gateway-redis    1
        Start Local Port Forwards
    END

Gateway Service Survives One Replica Restart
    TRY
        Scale Deployment And Wait Available    ${GATEWAY_NAMESPACE}    xtrinode-gateway    2
        Start Local Port Forwards
        ${before}=    Set Variable    /tmp/xtrinode-gateway-redundancy-before.json
        Gateway Info Should Return Status    ${before}    200
        ${pod}=    Kubectl Output    get    pods    -n    ${GATEWAY_NAMESPACE}    -l    app.kubernetes.io/component=gateway    -o    jsonpath={.items[0].metadata.name}
        Command Should Succeed    kubectl    delete    pod    ${pod}    -n    ${GATEWAY_NAMESPACE}    --wait=false
        Start Local Port Forwards
        ${during}=    Set Variable    /tmp/xtrinode-gateway-redundancy-during.json
        Wait Until Keyword Succeeds    180s    2s    Gateway Info Should Return Status    ${during}    200
        Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    Deployment Should Be Available    ${GATEWAY_NAMESPACE}    xtrinode-gateway    2
    FINALLY
        Scale Deployment And Wait Available    ${GATEWAY_NAMESPACE}    xtrinode-gateway    1
        Start Local Port Forwards
    END
