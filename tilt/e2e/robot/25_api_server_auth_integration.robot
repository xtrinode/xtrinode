*** Settings ***
Documentation       API server authentication integration tests against the local Tilt/k3d deployment.
Resource            resources/local.resource
Suite Setup         Start Local Port Forwards
Suite Teardown      Stop Local Port Forwards
Test Tags           local    k3d    integration    auth    api
Test Teardown       Run Keyword If Test Failed    Dump Debug

*** Test Cases ***
API Server Auth Is Wired In Deployed Control Plane
    Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    Deployment Should Be Available    ${API_SERVER_NAMESPACE}    xtrinode-api-server    1
    Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    Deployment Should Be Available    ${GATEWAY_NAMESPACE}    xtrinode-gateway    1
    Kubectl Output    get    secret    xtrinode-api-server-auth    -n    ${API_SERVER_NAMESPACE}
    Kubectl Output    get    secret    xtrinode-api-server-resume-auth    -n    ${API_SERVER_NAMESPACE}
    Kubectl Output    get    secret    xtrinode-gateway-api-server-auth    -n    ${GATEWAY_NAMESPACE}
    ${api_args}=    Kubectl Output    get    deployment    xtrinode-api-server    -n    ${API_SERVER_NAMESPACE}    -o    jsonpath={.spec.template.spec.containers[0].args}
    Should Contain    ${api_args}    --auth-enabled=true
    Should Contain    ${api_args}    --auth-token-file=/var/run/xtrinode-api-server-auth/token
    Should Contain    ${api_args}    --resume-auth-token-file=/var/run/xtrinode-api-server-resume-auth/token
    ${gateway_args}=    Kubectl Output    get    deployment    xtrinode-gateway    -n    ${GATEWAY_NAMESPACE}    -o    jsonpath={.spec.template.spec.containers[0].args}
    Should Contain    ${gateway_args}    --api-server-auth-token-file=/var/run/xtrinode-api-server-auth/token

API Server Probe Endpoints Do Not Require Bearer Token
    ${health}=    HTTP Request To File    GET    http://127.0.0.1:${API_SERVER_PORT}/health    /tmp/xtrinode-api-auth-health.txt
    Should Be Equal    ${health}    200
    ${metrics}=    HTTP Request To File    GET    http://127.0.0.1:${API_SERVER_PORT}/metrics    /tmp/xtrinode-api-auth-prometheus.txt
    Should Be Equal    ${metrics}    200

API Server Rejects Missing And Wrong Bearer Tokens
    ${missing_body}=    Set Variable    /tmp/xtrinode-api-auth-missing.json
    ${missing}=    Run Process    curl    -sS    -o    ${missing_body}    -w    ${CURL_HTTP_CODE_FORMAT}    http://127.0.0.1:${API_SERVER_PORT}/api/v1/runtimes    stderr=STDOUT
    Log    ${missing.stdout}
    Should Be Equal As Integers    ${missing.rc}    0    msg=${missing.stdout}
    ${missing_status}=    Strip String    ${missing.stdout}
    Should Be Equal    ${missing_status}    401
    JQ Should Match    ${missing_body}    .code == "UNAUTHORIZED"
    ${wrong_body}=    Set Variable    /tmp/xtrinode-api-auth-wrong.json
    ${wrong}=    Run Process    curl    -sS    -o    ${wrong_body}    -w    ${CURL_HTTP_CODE_FORMAT}    -H    Authorization: Bearer wrong-token    http://127.0.0.1:${API_SERVER_PORT}/api/v1/runtimes    stderr=STDOUT
    Log    ${wrong.stdout}
    Should Be Equal As Integers    ${wrong.rc}    0    msg=${wrong.stdout}
    ${wrong_status}=    Strip String    ${wrong.stdout}
    Should Be Equal    ${wrong_status}    401
    JQ Should Match    ${wrong_body}    .code == "UNAUTHORIZED"

API Server Accepts Configured Bearer Token
    ${body}=    Set Variable    /tmp/xtrinode-api-auth-authorized.json
    ${status}=    HTTP Request To File    GET    http://127.0.0.1:${API_SERVER_PORT}/api/v1/runtimes    ${body}
    Should Be Equal    ${status}    200
    JQ Should Match    ${body}    type == "array"

API Server Resume Token Is Least Privilege
    ${forbidden_body}=    Set Variable    /tmp/xtrinode-api-auth-resume-token-forbidden.json
    ${forbidden}=    Run Process    curl    -sS    -o    ${forbidden_body}    -w    ${CURL_HTTP_CODE_FORMAT}    -H    Authorization: Bearer ${API_SERVER_RESUME_AUTH_TOKEN}    http://127.0.0.1:${API_SERVER_PORT}/api/v1/runtimes    stderr=STDOUT
    Log    ${forbidden.stdout}
    Should Be Equal As Integers    ${forbidden.rc}    0    msg=${forbidden.stdout}
    ${forbidden_status}=    Strip String    ${forbidden.stdout}
    Should Be Equal    ${forbidden_status}    403
    JQ Should Match    ${forbidden_body}    .code == "FORBIDDEN"
    ${resume_body}=    Set Variable    /tmp/xtrinode-api-auth-resume-token-allowed.json
    ${resume}=    Run Process    curl    -sS    -o    ${resume_body}    -w    ${CURL_HTTP_CODE_FORMAT}    -X    POST    -H    Authorization: Bearer ${API_SERVER_RESUME_AUTH_TOKEN}    -H    Content-Type: application/json    --data    {}    http://127.0.0.1:${API_SERVER_PORT}/api/v1/resume    stderr=STDOUT
    Log    ${resume.stdout}
    Should Be Equal As Integers    ${resume.rc}    0    msg=${resume.stdout}
    ${resume_status}=    Strip String    ${resume.stdout}
    Should Be Equal    ${resume_status}    400
    JQ Should Match    ${resume_body}    .code == "INVALID_REQUEST"

API Server Does Not Emit Wildcard CORS For Control Plane API
    ${body}=    Set Variable    /tmp/xtrinode-api-auth-cors.json
    ${headers}=    Set Variable    /tmp/xtrinode-api-auth-cors.headers
    ${status}=    HTTP Request With Headers To File    GET    http://127.0.0.1:${API_SERVER_PORT}/api/v1/runtimes    ${body}    ${headers}    ${EMPTY}    Origin: https://untrusted.example
    Should Be Equal    ${status}    200
    ${headers_text}=    Get File    ${headers}
    Should Not Contain    ${headers_text}    Access-Control-Allow-Origin: *
    Should Not Contain    ${headers_text}    Access-Control-Allow-Origin: https://untrusted.example
