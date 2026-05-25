*** Settings ***
Documentation       Local Postgres catalog integration: fixture, catalog mount, secret env, and gateway query.
Resource            resources/local.resource
Suite Setup         Run Keywords    Ensure Local XTrinode Ready    AND    Start Local Port Forwards    AND    Wait For Gateway Backend Ready
Suite Teardown      Stop Local Port Forwards
Test Tags           local    k3d    postgres    catalog    integration
Test Teardown       Run Keyword If Test Failed    Dump Debug

*** Test Cases ***
Postgres Catalog ConfigMap Is Rendered With Secret Placeholder
    ${properties}=    Kubectl Output    get    configmap    trino-catalog-postgres    -n    ${NAMESPACE}    -o    ${POSTGRES_CATALOG_PROPERTIES_OUTPUT}
    Should Contain    ${properties}    connector.name=postgresql
    Should Contain    ${properties}    connection-url=jdbc:postgresql://postgres.team-local.svc.cluster.local:5432/analytics
    Should Contain    ${properties}    connection-user=trino
    Should Contain    ${properties}    connection-password=
    Should Contain    ${properties}    CATALOG_POSTGRES_CONNECTION_PASSWORD
    Should Not Contain    ${properties}    connection-password=trino
    Should Not Contain    ${properties}    postgres-credentials

Postgres Catalog Is Mounted Into Trino Pods
    ${coordinator}=    Kubectl Output    get    deployment    trino-${XTRINODE_NAME}-coordinator    -n    ${NAMESPACE}    -o    json
    ${worker}=    Kubectl Output    get    deployment    trino-${XTRINODE_NAME}-worker    -n    ${NAMESPACE}    -o    json
    Create File    /tmp/xtrinode-postgres-coordinator.json    ${coordinator}
    Create File    /tmp/xtrinode-postgres-worker.json    ${worker}
    JQ Should Match    /tmp/xtrinode-postgres-coordinator.json    any(.spec.template.spec.volumes[]?; .name == "catalog-volume" and any(.projected.sources[]?; .configMap.name == "trino-catalog-postgres")) and any(.spec.template.spec.containers[0].volumeMounts[]?; .name == "catalog-volume" and .mountPath == "/etc/trino/catalog")
    JQ Should Match    /tmp/xtrinode-postgres-worker.json    any(.spec.template.spec.volumes[]?; .name == "catalog-volume" and any(.projected.sources[]?; .configMap.name == "trino-catalog-postgres")) and any(.spec.template.spec.containers[0].volumeMounts[]?; .name == "catalog-volume" and .mountPath == "/etc/trino/catalog")

Postgres Catalog Password Secret Is Injected Into Trino Pods
    ${coordinator}=    Kubectl Output    get    deployment    trino-${XTRINODE_NAME}-coordinator    -n    ${NAMESPACE}    -o    json
    ${worker}=    Kubectl Output    get    deployment    trino-${XTRINODE_NAME}-worker    -n    ${NAMESPACE}    -o    json
    Create File    /tmp/xtrinode-postgres-secret-env-coordinator.json    ${coordinator}
    Create File    /tmp/xtrinode-postgres-secret-env-worker.json    ${worker}
    JQ Should Match    /tmp/xtrinode-postgres-secret-env-coordinator.json    any(.spec.template.spec.containers[0].env[]?; .name == "CATALOG_POSTGRES_CONNECTION_PASSWORD" and .valueFrom.secretKeyRef.name == "postgres-credentials" and .valueFrom.secretKeyRef.key == "password")
    JQ Should Match    /tmp/xtrinode-postgres-secret-env-worker.json    any(.spec.template.spec.containers[0].env[]?; .name == "CATALOG_POSTGRES_CONNECTION_PASSWORD" and .valueFrom.secretKeyRef.name == "postgres-credentials" and .valueFrom.secretKeyRef.key == "password")

Postgres Query Runs Through Gateway
    ${count_body}=    Set Variable    /tmp/xtrinode-postgres-count-query.json
    Gateway Statement Should Return Status    ${count_body}    SELECT count(*) FROM postgres.public.orders    200
    Drain Trino Query Preserving Data    ${count_body}
    JQ Should Match    ${count_body}    .data[0][0] == 3
    ${sum_body}=    Set Variable    /tmp/xtrinode-postgres-sum-query.json
    Gateway Statement Should Return Status    ${sum_body}    SELECT sum(amount) FROM postgres.public.orders    200
    Drain Trino Query Preserving Data    ${sum_body}
    JQ Should Match    ${sum_body}    .data[0][0] == 400

Postgres Query Error Propagates Through Gateway
    ${body}=    Set Variable    /tmp/xtrinode-postgres-missing-table-query.json
    Gateway Statement Should Return Status    ${body}    SELECT count(*) FROM postgres.public.missing_orders    200
    Trino Query Should Fail With    ${body}    missing_orders

Postgres Catalog Modification Rolls Coordinator And Worker
    ${coordinator_before}=    Kubectl Output    get    deployment    trino-${XTRINODE_NAME}-coordinator    -n    ${NAMESPACE}    -o    ${COORDINATOR_ROLLOUT_HASH_OUTPUT}
    ${worker_before}=    Kubectl Output    get    deployment    trino-${XTRINODE_NAME}-worker    -n    ${NAMESPACE}    -o    ${WORKER_ROLLOUT_HASH_OUTPUT}
    ${patch}=    Set Variable    {"spec":{"connector":{"postgres":{"properties":{"case-insensitive-name-matching":"false"}}}}}
    Command Should Succeed    kubectl    patch    xtrinodecatalog/postgres    -n    ${NAMESPACE}    --type=merge    -p    ${patch}
    Wait Until Keyword Succeeds    180s    ${POLL_INTERVAL}    Postgres Catalog Properties Should Contain    case-insensitive-name-matching=false
    Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    Deployment Pod Template Annotation Should Not Equal    ${NAMESPACE}    trino-${XTRINODE_NAME}-coordinator    ${COORDINATOR_ROLLOUT_HASH_OUTPUT}    ${coordinator_before}
    Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    Deployment Pod Template Annotation Should Not Equal    ${NAMESPACE}    trino-${XTRINODE_NAME}-worker    ${WORKER_ROLLOUT_HASH_OUTPUT}    ${worker_before}
    Command Should Succeed    kubectl    rollout    status    deployment/trino-${XTRINODE_NAME}-coordinator    -n    ${NAMESPACE}    --timeout=300s
    Command Should Succeed    kubectl    rollout    status    deployment/trino-${XTRINODE_NAME}-worker    -n    ${NAMESPACE}    --timeout=300s
    Wait For Gateway Backend Ready
    ${body}=    Set Variable    /tmp/xtrinode-postgres-count-query-after-catalog-update.json
    Gateway Statement Should Return Status    ${body}    SELECT count(*) FROM postgres.public.orders    200
    Drain Trino Query Preserving Data    ${body}
    JQ Should Match    ${body}    .data[0][0] == 3

*** Keywords ***
Postgres Catalog Properties Should Contain
    [Arguments]    ${expected}
    ${properties}=    Kubectl Output    get    configmap    trino-catalog-postgres    -n    ${NAMESPACE}    -o    ${POSTGRES_CATALOG_PROPERTIES_OUTPUT}
    Should Contain    ${properties}    ${expected}

Drain Trino Query Preserving Data
    [Arguments]    ${body_file}
    ${last_data}=    Set Variable    ${EMPTY}
    FOR    ${index}    IN RANGE    60
        ${data}=    Command Should Succeed    jq    -c    .data // empty    ${body_file}
        IF    '${data}' != ''
            ${last_data}=    Set Variable    ${data}
        END
        ${state}=    Command Should Succeed    jq    -r    .stats.state // .state // empty    ${body_file}
        IF    '${state}' == 'FINISHED'
            IF    '${last_data}' != ''
                ${merged}=    Command Should Succeed    jq    --argjson    data    ${last_data}    .data = $data    ${body_file}
                Create File    ${body_file}    ${merged}
            END
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

Trino Query Should Fail With
    [Arguments]    ${body_file}    ${expected_text}
    FOR    ${index}    IN RANGE    60
        ${state}=    Command Should Succeed    jq    -r    .stats.state // .state // empty    ${body_file}
        IF    '${state}' == 'FAILED'
            ${body}=    Get File    ${body_file}
            Should Contain    ${body}    ${expected_text}
            RETURN
        END
        IF    '${state}' == 'FINISHED'
            ${body}=    Get File    ${body_file}
            Fail    Expected Trino query to fail, but it finished successfully: ${body}
        END
        ${next_uri}=    Command Should Succeed    jq    -r    .nextUri // empty    ${body_file}
        IF    '${next_uri}' == ''
            ${body}=    Get File    ${body_file}
            Fail    Trino query did not fail and did not return nextUri: ${body}
        END
        ${next_path}=    Evaluate    __import__('urllib.parse').parse.urlparse($next_uri).path
        Wait Until Keyword Succeeds    45s    2s    HTTP Request To File Should Return Status    GET    http://127.0.0.1:${GATEWAY_PORT}${next_path}    ${body_file}    200    ${EMPTY}    X-Trino-User: local-e2e-contracts    X-Trino-XTrinode: ${XTRINODE_NAME}
        Sleep    1s
    END
    ${body}=    Get File    ${body_file}
    Fail    Timed out waiting for Trino query failure: ${body}
