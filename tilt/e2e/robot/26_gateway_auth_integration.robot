*** Settings ***
Documentation       Live gateway API-key authentication coverage against the local real-Trino deployment.
Resource            resources/local.resource
Suite Setup         Run Keywords    Ensure Local XTrinode Ready    AND    Setup Gateway API Key Auth Suite
Suite Teardown      Teardown Gateway API Key Auth Suite
Test Teardown       Run Keyword If Test Failed    Dump Debug
Test Tags           local    k3d    integration    gateway    auth    gateway-auth

*** Variables ***
${GATEWAY_AUTH_SECRET_NAME}     trino-gateway-api-keys
${GATEWAY_AUTH_SECRET_KEY}      api-keys
${GATEWAY_AUTH_KEY_ID}          local-e2e
${GATEWAY_AUTH_VALID_KEY}       local-gateway-api-key-1234567890
${GATEWAY_AUTH_RBAC_NAME}       xtrinode-gateway-e2e-auth-secret
${GATEWAY_AUTH_ARGS_PATCH}      /tmp/xtrinode-gateway-auth-args-patch.json

*** Test Cases ***
Gateway API Key Auth Is Wired In Deployed Gateway
    ${args}=    Kubectl Output    get    deployment    ${GATEWAY_SERVICE}    -n    ${GATEWAY_NAMESPACE}    -o    jsonpath={.spec.template.spec.containers[0].args}
    Should Contain    ${args}    --auth-enabled=true
    Should Contain    ${args}    --auth-type=api-key
    Should Contain    ${args}    --auth-secret-name=${GATEWAY_AUTH_SECRET_NAME}
    Should Contain    ${args}    --auth-secret-key=${GATEWAY_AUTH_SECRET_KEY}
    Should Contain    ${args}    --auth-namespace=${GATEWAY_NAMESPACE}
    ${service_account}=    Gateway Service Account
    ${can_get_secret}=    Command Should Succeed    kubectl    auth    can-i    get    secret/${GATEWAY_AUTH_SECRET_NAME}    -n    ${GATEWAY_NAMESPACE}    --as=system:serviceaccount:${GATEWAY_NAMESPACE}:${service_account}
    Should Be Equal    ${can_get_secret}    yes

Gateway Health Remains Public When API Key Auth Is Enabled
    HTTP Should Succeed    http://127.0.0.1:${GATEWAY_PORT}/health

Gateway Rejects Missing And Wrong API Keys
    Gateway API Key Request Should Return Status    missing    ${EMPTY}    401
    Gateway API Key Request Should Return Status    wrong    wrong-gateway-api-key-1234567890    401

Gateway Accepts Valid API Key And Proxies Trino
    Wait Until Keyword Succeeds    90s    2s    Gateway API Key Request Should Return Status    valid    ${GATEWAY_AUTH_VALID_KEY}    200
    ${body}=    Set Variable    /tmp/xtrinode-gateway-auth-statement.json
    Authenticated Gateway Statement Should Return Status    ${body}    SELECT 1    200
    Drain Authenticated Trino Query    ${body}

*** Keywords ***
Setup Gateway API Key Auth Suite
    Save Original Gateway Args
    Create Gateway API Key Secret
    Create Gateway Auth RBAC
    Enable Gateway API Key Auth
    Start Local Port Forwards

Teardown Gateway API Key Auth Suite
    Restore Gateway Args
    Stop Local Port Forwards
    Run Command Allow Failure    kubectl    delete    rolebinding    ${GATEWAY_AUTH_RBAC_NAME}    -n    ${GATEWAY_NAMESPACE}    --ignore-not-found=true
    Run Command Allow Failure    kubectl    delete    role    ${GATEWAY_AUTH_RBAC_NAME}    -n    ${GATEWAY_NAMESPACE}    --ignore-not-found=true
    Run Command Allow Failure    kubectl    delete    secret    ${GATEWAY_AUTH_SECRET_NAME}    -n    ${GATEWAY_NAMESPACE}    --ignore-not-found=true

Save Original Gateway Args
    ${args}=    Kubectl Output    get    deployment    ${GATEWAY_SERVICE}    -n    ${GATEWAY_NAMESPACE}    -o    jsonpath={.spec.template.spec.containers[0].args}
    Set Suite Variable    ${ORIGINAL_GATEWAY_ARGS}    ${args}

Restore Gateway Args
    Run Keyword And Ignore Error    Patch Gateway Args From JSON    ${ORIGINAL_GATEWAY_ARGS}
    Run Keyword And Ignore Error    Command Should Succeed    kubectl    rollout    status    deployment/${GATEWAY_SERVICE}    -n    ${GATEWAY_NAMESPACE}    --timeout=180s
    Start Local Port Forwards

Create Gateway API Key Secret
    ${secret_yaml}=    Command Should Succeed    kubectl    create    secret    generic    ${GATEWAY_AUTH_SECRET_NAME}    -n    ${GATEWAY_NAMESPACE}    --from-literal=${GATEWAY_AUTH_SECRET_KEY}=${GATEWAY_AUTH_KEY_ID}: ${GATEWAY_AUTH_VALID_KEY}    --dry-run=client    -o    yaml
    Create File    /tmp/xtrinode-gateway-auth-secret.yaml    ${secret_yaml}
    Command Should Succeed    kubectl    apply    -f    /tmp/xtrinode-gateway-auth-secret.yaml

Create Gateway Auth RBAC
    ${service_account}=    Gateway Service Account
    ${role_yaml}=    Command Should Succeed    kubectl    create    role    ${GATEWAY_AUTH_RBAC_NAME}    -n    ${GATEWAY_NAMESPACE}    --verb=get    --verb=list    --verb=watch    --resource=secrets    --resource-name=${GATEWAY_AUTH_SECRET_NAME}    --dry-run=client    -o    yaml
    Create File    /tmp/xtrinode-gateway-auth-role.yaml    ${role_yaml}
    Command Should Succeed    kubectl    apply    -f    /tmp/xtrinode-gateway-auth-role.yaml
    ${binding_yaml}=    Command Should Succeed    kubectl    create    rolebinding    ${GATEWAY_AUTH_RBAC_NAME}    -n    ${GATEWAY_NAMESPACE}    --role=${GATEWAY_AUTH_RBAC_NAME}    --serviceaccount=${GATEWAY_NAMESPACE}:${service_account}    --dry-run=client    -o    yaml
    Create File    /tmp/xtrinode-gateway-auth-rolebinding.yaml    ${binding_yaml}
    Command Should Succeed    kubectl    apply    -f    /tmp/xtrinode-gateway-auth-rolebinding.yaml

Enable Gateway API Key Auth
    Create File    /tmp/xtrinode-gateway-auth-original-args.json    ${ORIGINAL_GATEWAY_ARGS}
    Create File    /tmp/xtrinode-gateway-auth-filter.jq    map(select((startswith("--auth-enabled") or startswith("--auth-type=") or startswith("--auth-secret-name=") or startswith("--auth-secret-key=") or startswith("--auth-namespace=") or startswith("--auth-oauth-")) | not)) + ["--auth-enabled=true","--auth-type=api-key","--auth-secret-name=" + $secret,"--auth-secret-key=" + $key,"--auth-namespace=" + $namespace]
    ${patched_args}=    Run Process    jq    -c    --arg    secret    ${GATEWAY_AUTH_SECRET_NAME}    --arg    key    ${GATEWAY_AUTH_SECRET_KEY}    --arg    namespace    ${GATEWAY_NAMESPACE}    -f    /tmp/xtrinode-gateway-auth-filter.jq    /tmp/xtrinode-gateway-auth-original-args.json    stderr=STDOUT
    Should Be Equal As Integers    ${patched_args.rc}    0    msg=${patched_args.stdout}
    Patch Gateway Args From JSON    ${patched_args.stdout}
    Command Should Succeed    kubectl    rollout    status    deployment/${GATEWAY_SERVICE}    -n    ${GATEWAY_NAMESPACE}    --timeout=180s

Patch Gateway Args From JSON
    [Arguments]    ${args_json}
    Create File    /tmp/xtrinode-gateway-auth-args-to-patch.json    ${args_json}
    ${patch}=    Run Process    jq    -n    --slurpfile    args    /tmp/xtrinode-gateway-auth-args-to-patch.json    [{"op":"replace","path":"/spec/template/spec/containers/0/args","value":$args[0]}]    stdout=${GATEWAY_AUTH_ARGS_PATCH}    stderr=STDOUT
    Should Be Equal As Integers    ${patch.rc}    0    msg=${patch.stdout}
    Command Should Succeed    kubectl    patch    deployment    ${GATEWAY_SERVICE}    -n    ${GATEWAY_NAMESPACE}    --type=json    --patch-file    ${GATEWAY_AUTH_ARGS_PATCH}

Gateway Service Account
    ${service_account}=    Kubectl Output    get    deployment    ${GATEWAY_SERVICE}    -n    ${GATEWAY_NAMESPACE}    -o    jsonpath={.spec.template.spec.serviceAccountName}
    RETURN    ${service_account}

Gateway API Key Request Should Return Status
    [Arguments]    ${name}    ${api_key}    ${expected_status}
    ${body}=    Set Variable    /tmp/xtrinode-gateway-auth-${name}.json
    ${headers}=    Set Variable    /tmp/xtrinode-gateway-auth-${name}.headers
    ${status}=    Set Variable    ${EMPTY}
    IF    '${api_key}' == ''
        ${status}=    HTTP Request With Headers To File    GET    http://127.0.0.1:${GATEWAY_PORT}/v1/info    ${body}    ${headers}    ${EMPTY}    X-Trino-User: local-e2e-auth    X-Trino-XTrinode: ${XTRINODE_NAME}
    ELSE
        ${status}=    HTTP Request With Headers To File    GET    http://127.0.0.1:${GATEWAY_PORT}/v1/info    ${body}    ${headers}    ${EMPTY}    X-Trino-User: local-e2e-auth    X-Trino-XTrinode: ${XTRINODE_NAME}    X-API-Key: ${api_key}
    END
    Should Be Equal    ${status}    ${expected_status}
    IF    '${expected_status}' == '401'
        ${response_headers}=    Get File    ${headers}
        Should Contain    ${response_headers}    Authenticate: X-API-Key
    END

Authenticated Gateway Statement Should Return Status
    [Arguments]    ${body_file}    ${sql}    ${expected_status}
    ${status}=    HTTP Request To File    POST    http://127.0.0.1:${GATEWAY_PORT}/v1/statement    ${body_file}    ${sql}    X-Trino-User: local-e2e-auth    X-Trino-XTrinode: ${XTRINODE_NAME}    X-API-Key: ${GATEWAY_AUTH_VALID_KEY}
    Should Be Equal    ${status}    ${expected_status}

Drain Authenticated Trino Query
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
        Wait Until Keyword Succeeds    45s    2s    HTTP Request To File Should Return Status    GET    http://127.0.0.1:${GATEWAY_PORT}${next_path}    ${body_file}    200    ${EMPTY}    X-Trino-User: local-e2e-auth    X-Trino-XTrinode: ${XTRINODE_NAME}    X-API-Key: ${GATEWAY_AUTH_VALID_KEY}
        Sleep    1s
    END
    ${body}=    Get File    ${body_file}
    Fail    Timed out draining Trino query: ${body}
