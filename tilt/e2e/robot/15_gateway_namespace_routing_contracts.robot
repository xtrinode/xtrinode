*** Settings ***
Documentation       Gateway namespace routing contracts for same-name XTrinodes in different namespaces.
Resource            resources/local.resource
Suite Setup         Run Keywords    Ensure Namespace Routing Contract Prerequisites    AND    Start Local Port Forwards
Suite Teardown      Run Keywords    Stop Local Port Forwards    AND    Cleanup Namespace Routing Contract Objects
Test Tags           local    k3d    contracts    gateway    namespace-routing
Test Teardown       Run Keyword If Test Failed    Dump Namespace Routing Debug

*** Variables ***
${NAMESPACE_ROUTE_A}              team-a
${NAMESPACE_ROUTE_B}              team-b
${NAMESPACE_ROUTE_XTRINODE}       runtime
${NAMESPACE_ROUTE_HEADER_A}       team-a/runtime
${NAMESPACE_ROUTE_HEADER_B}       team-b/runtime
${NAMESPACE_ROUTE_GROUP_A}        team-a--runtime
${NAMESPACE_ROUTE_GROUP_B}        team-b--runtime
${NAMESPACE_ROUTE_LEASE_A}        xtrinode-resume-runtime-rt-team-a-runtime
${NAMESPACE_ROUTE_LEASE_B}        xtrinode-resume-runtime-rt-team-b-runtime

*** Test Cases ***
Gateway Routes Same Runtime Name By Namespace Qualified Defaults
    Gateway Namespace Routes Should Be Registered
    Gateway Namespace Route Should Resume Only Matching Namespace    ${NAMESPACE_ROUTE_HEADER_A}    ${NAMESPACE_ROUTE_A}    ${NAMESPACE_ROUTE_B}
    Gateway Namespace Route Should Resume Only Matching Namespace    ${NAMESPACE_ROUTE_HEADER_B}    ${NAMESPACE_ROUTE_B}    ${NAMESPACE_ROUTE_A}

*** Keywords ***
Ensure Namespace Routing Contract Prerequisites
    Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    Deployment Should Be Available    ${OPERATOR_NAMESPACE}    xtrinode-operator    1
    Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    Deployment Should Be Available    ${API_SERVER_NAMESPACE}    xtrinode-api-server    1
    Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    Deployment Should Be Available    ${GATEWAY_NAMESPACE}    xtrinode-gateway    1
    Cleanup Namespace Routing Contract Objects
    Create Namespace If Missing    ${NAMESPACE_ROUTE_A}
    Create Namespace If Missing    ${NAMESPACE_ROUTE_B}
    Apply Namespace Routing XTrinode    ${NAMESPACE_ROUTE_A}    ${NAMESPACE_ROUTE_HEADER_A}
    Apply Namespace Routing XTrinode    ${NAMESPACE_ROUTE_B}    ${NAMESPACE_ROUTE_HEADER_B}
    Wait Until Keyword Succeeds    180s    2s    Gateway Namespace Routes Should Be Registered

Create Namespace If Missing
    [Arguments]    ${namespace}
    ${result}=    Run Process    kubectl    create    namespace    ${namespace}    stderr=STDOUT
    Log    ${result.stdout}
    IF    ${result.rc} != 0
        Should Contain    ${result.stdout}    AlreadyExists
    END

Apply Namespace Routing XTrinode
    [Arguments]    ${namespace}    ${header}
    ${manifest}=    Set Variable    /tmp/xtrinode-namespace-routing-${namespace}.json
    ${json}=    Set Variable    {"apiVersion":"analytics.xtrinode.io/v1","kind":"XTrinode","metadata":{"name":"${NAMESPACE_ROUTE_XTRINODE}","namespace":"${namespace}","labels":{"test.xtrinode.io/contract":"namespace-routing"}},"spec":{"size":"xs","suspended":true,"routing":{"header":"X-Trino-XTrinode=${header}"}}}
    Create File    ${manifest}    ${json}
    Command Should Succeed    kubectl    apply    -f    ${manifest}

Gateway Namespace Routes Should Be Registered
    ${routes}=    Kubectl Output    get    configmap    trino-gateway-routes    -n    ${GATEWAY_NAMESPACE}    -o    ${GATEWAY_ROUTES_OUTPUT}
    Should Contain    ${routes}    routingGroup: ${NAMESPACE_ROUTE_GROUP_A}
    Should Contain    ${routes}    routingGroup: ${NAMESPACE_ROUTE_GROUP_B}
    Should Contain    ${routes}    header: ${NAMESPACE_ROUTE_HEADER_A}
    Should Contain    ${routes}    header: ${NAMESPACE_ROUTE_HEADER_B}
    Should Contain    ${routes}    namespace: ${NAMESPACE_ROUTE_A}
    Should Contain    ${routes}    namespace: ${NAMESPACE_ROUTE_B}
    Should Contain    ${routes}    coordinatorURL: http://trino-${NAMESPACE_ROUTE_XTRINODE}.${NAMESPACE_ROUTE_A}.svc.cluster.local:8080
    Should Contain    ${routes}    coordinatorURL: http://trino-${NAMESPACE_ROUTE_XTRINODE}.${NAMESPACE_ROUTE_B}.svc.cluster.local:8080
    Should Not Contain    ${routes}    routingGroup: ${NAMESPACE_ROUTE_XTRINODE}

Gateway Namespace Route Should Resume Only Matching Namespace
    [Arguments]    ${header}    ${expected_namespace}    ${other_namespace}
    Clear Namespace Routing Resume Leases
    ${body}=    Set Variable    /tmp/xtrinode-namespace-routing-${expected_namespace}-gateway.json
    Wait Until Keyword Succeeds    90s    2s    Gateway Namespace Route Should Trigger Resume    ${header}    ${body}
    Wait Until Keyword Succeeds    90s    2s    Namespace Routing Resume Lease Should Exist    ${expected_namespace}
    Namespace Routing Resume Lease Should Not Exist    ${other_namespace}

Gateway Namespace Route Should Trigger Resume
    [Arguments]    ${header}    ${body_file}
    ${status}=    HTTP Request To File    GET    http://127.0.0.1:${GATEWAY_PORT}/v1/info    ${body_file}    ${EMPTY}    X-Trino-User: namespace-routing-contract    X-Trino-XTrinode: ${header}
    Should Be Equal    ${status}    503
    JQ Should Match    ${body_file}    (.triggered == true) or (.gated == true)

Namespace Routing Resume Lease Should Exist
    [Arguments]    ${namespace}
    ${lease}=    Namespace Routing Lease Name    ${namespace}
    Kubectl Output    get    lease    ${lease}    -n    ${OPERATOR_NAMESPACE}

Namespace Routing Resume Lease Should Not Exist
    [Arguments]    ${namespace}
    ${lease}=    Namespace Routing Lease Name    ${namespace}
    ${result}=    Run Command Allow Failure    kubectl    get    lease    ${lease}    -n    ${OPERATOR_NAMESPACE}
    Should Not Be Equal As Integers    ${result.rc}    0

Namespace Routing Lease Name
    [Arguments]    ${namespace}
    IF    '${namespace}' == '${NAMESPACE_ROUTE_A}'
        RETURN    ${NAMESPACE_ROUTE_LEASE_A}
    END
    IF    '${namespace}' == '${NAMESPACE_ROUTE_B}'
        RETURN    ${NAMESPACE_ROUTE_LEASE_B}
    END
    Fail    Unknown namespace routing contract namespace: ${namespace}

Clear Namespace Routing Resume Leases
    Run Command Allow Failure    kubectl    delete    lease    ${NAMESPACE_ROUTE_LEASE_A}    -n    ${OPERATOR_NAMESPACE}    --ignore-not-found=true
    Run Command Allow Failure    kubectl    delete    lease    ${NAMESPACE_ROUTE_LEASE_B}    -n    ${OPERATOR_NAMESPACE}    --ignore-not-found=true

Cleanup Namespace Routing Contract Objects
    Run Command Allow Failure    kubectl    patch    xtrinode/${NAMESPACE_ROUTE_XTRINODE}    -n    ${NAMESPACE_ROUTE_A}    --type=merge    -p    {"metadata":{"finalizers":[]}}
    Run Command Allow Failure    kubectl    patch    xtrinode/${NAMESPACE_ROUTE_XTRINODE}    -n    ${NAMESPACE_ROUTE_B}    --type=merge    -p    {"metadata":{"finalizers":[]}}
    Run Command Allow Failure    kubectl    delete    xtrinode/${NAMESPACE_ROUTE_XTRINODE}    -n    ${NAMESPACE_ROUTE_A}    --wait=false    --ignore-not-found=true
    Run Command Allow Failure    kubectl    delete    xtrinode/${NAMESPACE_ROUTE_XTRINODE}    -n    ${NAMESPACE_ROUTE_B}    --wait=false    --ignore-not-found=true
    ${wait_a}=    Run Command Allow Failure    kubectl    wait    xtrinode/${NAMESPACE_ROUTE_XTRINODE}    -n    ${NAMESPACE_ROUTE_A}    --for=delete    --timeout=120s
    ${wait_b}=    Run Command Allow Failure    kubectl    wait    xtrinode/${NAMESPACE_ROUTE_XTRINODE}    -n    ${NAMESPACE_ROUTE_B}    --for=delete    --timeout=120s
    IF    ${wait_a.rc} != 0
        Run Command Allow Failure    kubectl    patch    xtrinode/${NAMESPACE_ROUTE_XTRINODE}    -n    ${NAMESPACE_ROUTE_A}    --type=merge    -p    {"metadata":{"finalizers":[]}}
        Run Command Allow Failure    kubectl    delete    xtrinode/${NAMESPACE_ROUTE_XTRINODE}    -n    ${NAMESPACE_ROUTE_A}    --wait=false    --ignore-not-found=true
        Wait Until Keyword Succeeds    60s    2s    Namespace Routing XTrinode Should Not Exist    ${NAMESPACE_ROUTE_A}
    END
    IF    ${wait_b.rc} != 0
        Run Command Allow Failure    kubectl    patch    xtrinode/${NAMESPACE_ROUTE_XTRINODE}    -n    ${NAMESPACE_ROUTE_B}    --type=merge    -p    {"metadata":{"finalizers":[]}}
        Run Command Allow Failure    kubectl    delete    xtrinode/${NAMESPACE_ROUTE_XTRINODE}    -n    ${NAMESPACE_ROUTE_B}    --wait=false    --ignore-not-found=true
        Wait Until Keyword Succeeds    60s    2s    Namespace Routing XTrinode Should Not Exist    ${NAMESPACE_ROUTE_B}
    END
    Clear Namespace Routing Resume Leases
    Remove Namespace Routing Routes From Gateway ConfigMap

Namespace Routing XTrinode Should Not Exist
    [Arguments]    ${namespace}
    ${result}=    Run Process    kubectl    get    xtrinode/${NAMESPACE_ROUTE_XTRINODE}    -n    ${namespace}    stderr=STDOUT
    Log    ${result.stdout}
    Should Not Be Equal As Integers    ${result.rc}    0    msg=${result.stdout}

Remove Namespace Routing Routes From Gateway ConfigMap
    ${routes}=    Kubectl Output    get    configmap    trino-gateway-routes    -n    ${GATEWAY_NAMESPACE}    -o    ${GATEWAY_ROUTES_OUTPUT}
    ${routes_file}=    Set Variable    /tmp/xtrinode-namespace-routing-routes.yaml
    ${filtered_file}=    Set Variable    /tmp/xtrinode-namespace-routing-routes-filtered.yaml
    ${patch_file}=    Set Variable    /tmp/xtrinode-namespace-routing-routes-patch.json
    Create File    ${routes_file}    ${routes}
    ${filtered}=    Command Should Succeed    yq    -y    (.routes // []) |= map(select(.routingGroup != "team-a--runtime" and .routingGroup != "team-b--runtime"))    ${routes_file}
    Create File    ${filtered_file}    ${filtered}
    ${patch}=    Command Should Succeed    jq    -n    --arg    routes    ${filtered}    [{"op":"replace","path":"/data/routes.yaml","value":$routes}]
    Create File    ${patch_file}    ${patch}
    Command Should Succeed    kubectl    patch    configmap    trino-gateway-routes    -n    ${GATEWAY_NAMESPACE}    --type=json    --patch-file    ${patch_file}

Dump Namespace Routing Debug
    Dump Debug
    Run Command Allow Failure    kubectl    get    xtrinode    ${NAMESPACE_ROUTE_XTRINODE}    -n    ${NAMESPACE_ROUTE_A}    -o    yaml
    Run Command Allow Failure    kubectl    get    xtrinode    ${NAMESPACE_ROUTE_XTRINODE}    -n    ${NAMESPACE_ROUTE_B}    -o    yaml
    Run Command Allow Failure    kubectl    get    events    -n    ${NAMESPACE_ROUTE_A}    --sort-by=.lastTimestamp
    Run Command Allow Failure    kubectl    get    events    -n    ${NAMESPACE_ROUTE_B}    --sort-by=.lastTimestamp
