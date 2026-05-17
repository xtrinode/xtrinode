*** Settings ***
Documentation       Full real-Trino lifecycle smoke: query, suspend to zero, gateway/API resume, and lease gating.
Resource            resources/local.resource
Test Tags           local    k3d    smoke    lifecycle    trino    keda
Test Teardown       Run Keyword If Test Failed    Dump Debug

*** Test Cases ***
Real Trino Suspend Resume And Lease Gating Works
    File Should Exist    ${SMOKE_SCRIPT}
    ${result}=    Run Process    ${SMOKE_SCRIPT}
    ...    env:NAMESPACE=${NAMESPACE}
    ...    env:XTRINODE_NAME=${XTRINODE_NAME}
    ...    env:OPERATOR_NAMESPACE=${OPERATOR_NAMESPACE}
    ...    env:GATEWAY_NAMESPACE=${GATEWAY_NAMESPACE}
    ...    env:API_SERVER_NAMESPACE=${API_SERVER_NAMESPACE}
    ...    env:TRINO_IMAGE_REPOSITORY=${TRINO_IMAGE_REPOSITORY}
    ...    env:TRINO_IMAGE_TAG=${TRINO_IMAGE_TAG}
    ...    stderr=STDOUT
    Log    ${result.stdout}
    Should Be Equal As Integers    ${result.rc}    0    msg=${result.stdout}
