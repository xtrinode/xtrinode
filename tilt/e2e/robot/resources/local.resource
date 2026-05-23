*** Settings ***
Library           Collections
Library           OperatingSystem
Library           Process
Library           String

*** Variables ***
${NAMESPACE}                 team-local
${XTRINODE_NAME}             local-trino-keda
${OPERATOR_NAMESPACE}        xtrinode-system
${GATEWAY_NAMESPACE}         xtrinode-gateway
${API_SERVER_NAMESPACE}      xtrinode-system
${GATEWAY_SERVICE}           xtrinode-gateway
${API_SERVER_SERVICE}        xtrinode-api-server
${GATEWAY_PORT}              18080
${API_SERVER_PORT}           18081
${API_SERVER_AUTH_TOKEN}     local-dev-api-server-token
${API_SERVER_RESUME_AUTH_TOKEN}    local-dev-api-server-resume-token
${WAIT_TIMEOUT}              900s
${POLL_INTERVAL}             5s
${TRINO_IMAGE_REPOSITORY}    trinodb/trino
${TRINO_IMAGE_TAG}           480
${REPO_ROOT}                 ${CURDIR}/../../../..
${MANIFEST}                  ${REPO_ROOT}/tilt/examples/xtrinode-real-trino-keda.yaml
${POSTGRES_MANIFEST}         ${REPO_ROOT}/tilt/examples/postgres-analytics.yaml
${SMOKE_SCRIPT}              ${REPO_ROOT}/tilt/e2e/helpers/local-trino-keda-smoke.sh
${GATEWAY_ROUTES_OUTPUT}     go-template={{ index .data "routes.yaml" }}
${CATALOG_PROPERTIES_OUTPUT}    go-template={{ index .data "local-tpch.properties" }}
${POSTGRES_CATALOG_PROPERTIES_OUTPUT}    go-template={{ index .data "postgres.properties" }}
${COORDINATOR_ROLLOUT_HASH_OUTPUT}    go-template={{ index .spec.template.metadata.annotations "trino.io/rollout-hash-coordinator" }}
${WORKER_ROLLOUT_HASH_OUTPUT}    go-template={{ index .spec.template.metadata.annotations "trino.io/rollout-hash-worker" }}
${COORDINATOR_TEMPLATE_REVISION_OUTPUT}    go-template={{ index .spec.template.metadata.annotations "xtrinode.io/revision" }}
${WORKER_TEMPLATE_REVISION_OUTPUT}    go-template={{ index .spec.template.metadata.annotations "xtrinode.io/revision" }}
${COORDINATOR_DEPLOYMENT_REVISION_OUTPUT}    go-template={{ index .metadata.annotations "deployment.kubernetes.io/revision" }}
${WORKER_DEPLOYMENT_REVISION_OUTPUT}    go-template={{ index .metadata.annotations "deployment.kubernetes.io/revision" }}
${COORDINATOR_CONFIG_CHECKSUM_OUTPUT}    go-template={{ index .spec.template.metadata.annotations "checksum/coordinator-config" }}
${WORKER_CONFIG_CHECKSUM_OUTPUT}    go-template={{ index .spec.template.metadata.annotations "checksum/worker-config" }}
${CURL_HTTP_CODE_FORMAT}     \%{http_code}

*** Keywords ***
Command Should Succeed
    [Arguments]    @{command}
    ${result}=    Run Process    @{command}    stderr=STDOUT
    Log    ${result.stdout}
    Should Be Equal As Integers    ${result.rc}    0    msg=${result.stdout}
    ${stdout}=    Strip String    ${result.stdout}
    RETURN    ${stdout}

Run Command Allow Failure
    [Arguments]    @{command}
    ${result}=    Run Process    @{command}    stderr=STDOUT
    Log    ${result.stdout}
    RETURN    ${result}

Kubectl Output
    [Arguments]    @{args}
    ${stdout}=    Command Should Succeed    kubectl    @{args}
    RETURN    ${stdout}

Create Local Namespace
    ${result}=    Run Process    kubectl    create    namespace    ${NAMESPACE}    stderr=STDOUT
    Log    ${result.stdout}
    IF    ${result.rc} != 0
        Should Contain    ${result.stdout}    AlreadyExists
    END

Reset Local XTrinode Resources
    Run Command Allow Failure    kubectl    patch    xtrinode/${XTRINODE_NAME}    -n    ${NAMESPACE}    --type=merge    -p    {"metadata":{"finalizers":[]}}
    Run Command Allow Failure    kubectl    delete    xtrinode/${XTRINODE_NAME}    -n    ${NAMESPACE}    --wait=false
    ${deleted}=    Run Command Allow Failure    kubectl    wait    xtrinode/${XTRINODE_NAME}    -n    ${NAMESPACE}    --for=delete    --timeout=120s
    IF    ${deleted.rc} != 0
        Run Command Allow Failure    kubectl    patch    xtrinode/${XTRINODE_NAME}    -n    ${NAMESPACE}    --type=merge    -p    {"metadata":{"finalizers":[]}}
        Run Command Allow Failure    kubectl    delete    xtrinode/${XTRINODE_NAME}    -n    ${NAMESPACE}    --wait=false    --ignore-not-found=true
        Wait Until Keyword Succeeds    60s    2s    Local XTrinode Should Not Exist
    END
    Run Command Allow Failure    kubectl    delete    xtrinodecatalog/local-tpch    -n    ${NAMESPACE}    --ignore-not-found=true
    Run Command Allow Failure    kubectl    delete    xtrinodecatalog/postgres    -n    ${NAMESPACE}    --ignore-not-found=true
    Run Command Allow Failure    kubectl    delete    lease    -n    ${OPERATOR_NAMESPACE}    -l    app.kubernetes.io/name=xtrinode-operator,app.kubernetes.io/component=api-server    --ignore-not-found=true

Local XTrinode Should Not Exist
    ${result}=    Run Process    kubectl    get    xtrinode/${XTRINODE_NAME}    -n    ${NAMESPACE}    stderr=STDOUT
    Log    ${result.stdout}
    Should Not Be Equal As Integers    ${result.rc}    0    msg=${result.stdout}

Ensure Local Postgres Ready
    Command Should Succeed    kubectl    apply    -f    ${POSTGRES_MANIFEST}
    Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    Deployment Should Be Available    ${NAMESPACE}    postgres    1

Apply Local XTrinode Manifest
    Command Should Succeed    kubectl    apply    -f    ${MANIFEST}
    ${patch}=    Set Variable    {"spec":{"valuesOverlay":{"image":{"repository":"${TRINO_IMAGE_REPOSITORY}","tag":"${TRINO_IMAGE_TAG}"}}}}
    Command Should Succeed    kubectl    patch    xtrinode/${XTRINODE_NAME}    -n    ${NAMESPACE}    --type=merge    -p    ${patch}

Ensure Local XTrinode Ready
    Create Local Namespace
    Ensure Local Postgres Ready
    Reset Local XTrinode Resources
    Apply Local XTrinode Manifest
    Command Should Succeed    kubectl    wait    xtrinode/${XTRINODE_NAME}    -n    ${NAMESPACE}    --for=condition=Ready=True    --timeout=${WAIT_TIMEOUT}
    Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    Command Should Succeed    kubectl    wait    scaledobject/trino-${XTRINODE_NAME}-workers    -n    ${NAMESPACE}    --for=condition=Ready=True    --timeout=30s
    Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    Deployment Should Be Available    ${NAMESPACE}    trino-${XTRINODE_NAME}-coordinator    1
    Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    Deployment Should Be Available    ${NAMESPACE}    trino-${XTRINODE_NAME}-worker    1
    Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    Gateway Route Should Be Registered

Deployment Should Be Available
    [Arguments]    ${namespace}    ${deployment}    ${minimum}
    ${available}=    Kubectl Output    get    deployment    ${deployment}    -n    ${namespace}    -o    jsonpath={.status.availableReplicas}
    ${available}=    Set Variable If    '${available}' == ''    0    ${available}
    Should Be True    int("""${available}""") >= int("""${minimum}""")

Deployment Available Replicas Should Equal
    [Arguments]    ${namespace}    ${deployment}    ${expected}
    ${available}=    Kubectl Output    get    deployment    ${deployment}    -n    ${namespace}    -o    jsonpath={.status.availableReplicas}
    ${available}=    Set Variable If    '${available}' == ''    0    ${available}
    Should Be Equal As Integers    ${available}    ${expected}

Deployment Spec Replicas Should Equal
    [Arguments]    ${namespace}    ${deployment}    ${expected}
    ${replicas}=    Kubectl Output    get    deployment    ${deployment}    -n    ${namespace}    -o    jsonpath={.spec.replicas}
    ${replicas}=    Set Variable If    '${replicas}' == ''    0    ${replicas}
    Should Be Equal As Integers    ${replicas}    ${expected}

ScaledObject Max Replicas Should Equal
    [Arguments]    ${namespace}    ${scaledobject}    ${expected}
    ${replicas}=    Kubectl Output    get    scaledobject    ${scaledobject}    -n    ${namespace}    -o    jsonpath={.spec.maxReplicaCount}
    Should Be Equal As Integers    ${replicas}    ${expected}

Deployment Pod Template Annotation Should Not Equal
    [Arguments]    ${namespace}    ${deployment}    ${output_format}    ${old_value}
    ${value}=    Kubectl Output    get    deployment    ${deployment}    -n    ${namespace}    -o    ${output_format}
    Should Not Be Equal    ${value}    ${old_value}

Deployment Pod Template Annotation Should Equal
    [Arguments]    ${namespace}    ${deployment}    ${output_format}    ${expected}
    ${value}=    Kubectl Output    get    deployment    ${deployment}    -n    ${namespace}    -o    ${output_format}
    Should Be Equal    ${value}    ${expected}

Deployment Annotation Should Equal
    [Arguments]    ${namespace}    ${deployment}    ${output_format}    ${expected}
    ${value}=    Kubectl Output    get    deployment    ${deployment}    -n    ${namespace}    -o    ${output_format}
    Should Be Equal    ${value}    ${expected}

Scale Deployment And Wait Available
    [Arguments]    ${namespace}    ${deployment}    ${replicas}
    Command Should Succeed    kubectl    scale    deployment/${deployment}    -n    ${namespace}    --replicas=${replicas}
    Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    Deployment Should Be Available    ${namespace}    ${deployment}    ${replicas}

Gateway Route Should Be Registered
    ${routes}=    Kubectl Output    get    configmap    trino-gateway-routes    -n    ${GATEWAY_NAMESPACE}    -o    ${GATEWAY_ROUTES_OUTPUT}
    Should Contain    ${routes}    name: ${XTRINODE_NAME}
    Should Contain    ${routes}    state: RUNNING

Gateway Route Should Have Routing Group
    [Arguments]    ${routing_group}
    ${routes}=    Kubectl Output    get    configmap    trino-gateway-routes    -n    ${GATEWAY_NAMESPACE}    -o    ${GATEWAY_ROUTES_OUTPUT}
    Should Contain    ${routes}    name: ${XTRINODE_NAME}
    Should Contain    ${routes}    routingGroup: ${routing_group}
    Should Contain    ${routes}    state: RUNNING

Start Local Port Forwards
    Stop Local Port Forwards
    Start Process    kubectl    port-forward    -n    ${GATEWAY_NAMESPACE}    svc/${GATEWAY_SERVICE}    ${GATEWAY_PORT}:8080    stdout=/tmp/xtrinode-robot-gateway-port-forward.log    stderr=STDOUT    alias=xtrinode-gateway-port-forward
    Start Process    kubectl    port-forward    -n    ${API_SERVER_NAMESPACE}    svc/${API_SERVER_SERVICE}    ${API_SERVER_PORT}:8081    stdout=/tmp/xtrinode-robot-api-port-forward.log    stderr=STDOUT    alias=xtrinode-api-port-forward
    Wait Until Keyword Succeeds    45s    1s    HTTP Should Succeed    http://127.0.0.1:${GATEWAY_PORT}/health
    Wait Until Keyword Succeeds    45s    1s    HTTP Should Succeed    http://127.0.0.1:${API_SERVER_PORT}/health

Wait For Gateway Backend Ready
    ${body}=    Set Variable    /tmp/xtrinode-gateway-backend-ready.json
    Wait Until Keyword Succeeds    180s    2s    Gateway Info Should Return Status    ${body}    200

Stop Local Port Forwards
    Run Keyword And Ignore Error    Terminate Process    xtrinode-gateway-port-forward    kill=True
    Run Keyword And Ignore Error    Terminate Process    xtrinode-api-port-forward    kill=True

HTTP Should Succeed
    [Arguments]    ${url}
    ${result}=    Run Process    curl    -fsS    ${url}    stderr=STDOUT
    Should Be Equal As Integers    ${result.rc}    0    msg=${result.stdout}

HTTP Request To File
    [Arguments]    ${method}    ${url}    ${output_file}    ${body}=${EMPTY}    @{headers}
    @{args}=    Create List    -sS    -o    ${output_file}    -w    ${CURL_HTTP_CODE_FORMAT}    -X    ${method}
    ${is_api_server_request}=    Evaluate    "127.0.0.1:${API_SERVER_PORT}/api/" in $url
    IF    ${is_api_server_request}
        Append To List    ${args}    -H    Authorization: Bearer ${API_SERVER_AUTH_TOKEN}
    END
    FOR    ${header}    IN    @{headers}
        Append To List    ${args}    -H    ${header}
    END
    ${has_body}=    Evaluate    len($body) > 0
    IF    ${has_body}
        Append To List    ${args}    --data    ${body}
    END
    Append To List    ${args}    ${url}
    ${result}=    Run Process    curl    @{args}    stderr=STDOUT
    Log    ${result.stdout}
    Should Be Equal As Integers    ${result.rc}    0    msg=${result.stdout}
    ${status}=    Strip String    ${result.stdout}
    RETURN    ${status}

HTTP Request To File Should Return Status
    [Arguments]    ${method}    ${url}    ${output_file}    ${expected_status}    ${body}=${EMPTY}    @{headers}
    ${status}=    HTTP Request To File    ${method}    ${url}    ${output_file}    ${body}    @{headers}
    Should Be Equal    ${status}    ${expected_status}
    RETURN    ${status}

HTTP Request With Headers To File
    [Arguments]    ${method}    ${url}    ${output_file}    ${headers_file}    ${body}=${EMPTY}    @{headers}
    @{args}=    Create List    -sS    -D    ${headers_file}    -o    ${output_file}    -w    ${CURL_HTTP_CODE_FORMAT}    -X    ${method}
    ${is_api_server_request}=    Evaluate    "127.0.0.1:${API_SERVER_PORT}/api/" in $url
    IF    ${is_api_server_request}
        Append To List    ${args}    -H    Authorization: Bearer ${API_SERVER_AUTH_TOKEN}
    END
    FOR    ${header}    IN    @{headers}
        Append To List    ${args}    -H    ${header}
    END
    ${has_body}=    Evaluate    len($body) > 0
    IF    ${has_body}
        Append To List    ${args}    --data    ${body}
    END
    Append To List    ${args}    ${url}
    ${result}=    Run Process    curl    @{args}    stderr=STDOUT
    Log    ${result.stdout}
    Should Be Equal As Integers    ${result.rc}    0    msg=${result.stdout}
    ${status}=    Strip String    ${result.stdout}
    RETURN    ${status}

Gateway Info Should Return Status
    [Arguments]    ${body_file}    ${expected_status}
    ${status}=    HTTP Request To File    GET    http://127.0.0.1:${GATEWAY_PORT}/v1/info    ${body_file}    ${EMPTY}    X-Trino-User: local-e2e-contracts    X-Trino-XTrinode: ${XTRINODE_NAME}
    Should Be Equal    ${status}    ${expected_status}

Gateway Statement Should Return Status
    [Arguments]    ${body_file}    ${sql}    ${expected_status}
    ${status}=    HTTP Request To File    POST    http://127.0.0.1:${GATEWAY_PORT}/v1/statement    ${body_file}    ${sql}    X-Trino-User: local-e2e-contracts    X-Trino-XTrinode: ${XTRINODE_NAME}
    Should Be Equal    ${status}    ${expected_status}

Redis Sticky Route Should Contain Local Backend
    [Arguments]    ${query_id}
    ${sticky}=    Kubectl Output    exec    -n    ${GATEWAY_NAMESPACE}    deployment/xtrinode-gateway-redis    --    redis-cli    GET    query:${query_id}
    Should Contain    ${sticky}    ${NAMESPACE}
    Should Contain    ${sticky}    ${XTRINODE_NAME}
    Should Contain    ${sticky}    trino-${XTRINODE_NAME}.${NAMESPACE}.svc.cluster.local:8080

Drain Trino Query
    [Arguments]    ${body_file}
    FOR    ${index}    IN RANGE    60
        ${state}=    Command Should Succeed    jq    -r    .stats.state // .state // empty    ${body_file}
        IF    '${state}' == 'FINISHED'
            RETURN
        END
        IF    '${state}' == 'FAILED' or '${state}' == 'CANCELED' or '${state}' == 'CANCELLED'
            ${body}=    Get File    ${body_file}
            Fail    Trino query ended with state ${state}: ${body}
        END
        ${next_uri}=    Command Should Succeed    jq    -r    .nextUri // empty    ${body_file}
        IF    '${next_uri}' == ''
            ${body}=    Get File    ${body_file}
            Fail    Trino query did not finish and did not return nextUri: ${body}
        END
        ${next_path}=    Evaluate    __import__('urllib.parse').parse.urlparse($next_uri).path
        Wait Until Keyword Succeeds    45s    2s    HTTP Request To File Should Return Status    GET    http://127.0.0.1:${GATEWAY_PORT}${next_path}    ${body_file}    200    ${EMPTY}    X-Trino-User: local-e2e-contracts    X-Trino-XTrinode: ${XTRINODE_NAME}
        Sleep    1s
    END
    ${body}=    Get File    ${body_file}
    Fail    Timed out draining Trino query: ${body}

JQ Should Match
    [Arguments]    ${file}    ${filter}    @{jq_args}
    ${result}=    Run Process    jq    -e    @{jq_args}    ${filter}    ${file}    stderr=STDOUT
    Log    ${result.stdout}
    Should Be Equal As Integers    ${result.rc}    0    msg=${result.stdout}

API Error Contract Should Hold
    [Arguments]    ${method}    ${path}    ${expected_status}    ${expected_code}    ${description}    ${body}=${EMPTY}
    ${url}=    Set Variable    http://127.0.0.1:${API_SERVER_PORT}${path}
    ${response}=    Set Variable    /tmp/xtrinode-api-error-${expected_code}.json
    ${status}=    HTTP Request To File    ${method}    ${url}    ${response}    ${body}    Content-Type: application/json
    Should Be Equal    ${status}    ${expected_status}    msg=${description}
    JQ Should Match    ${response}    .code == $code    --arg    code    ${expected_code}

Dump Debug
    Run Command Allow Failure    kubectl    get    pods    -n    ${OPERATOR_NAMESPACE}    -o    wide
    Run Command Allow Failure    kubectl    get    pods    -n    ${GATEWAY_NAMESPACE}    -o    wide
    Run Command Allow Failure    kubectl    get    pods    -n    ${NAMESPACE}    -o    wide
    Run Command Allow Failure    kubectl    get    xtrinode    ${XTRINODE_NAME}    -n    ${NAMESPACE}    -o    yaml
    Run Command Allow Failure    kubectl    get    events    -n    ${NAMESPACE}    --sort-by=.lastTimestamp
