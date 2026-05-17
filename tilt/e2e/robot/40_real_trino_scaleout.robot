*** Settings ***
Documentation       Real-Trino KEDA scale-out smoke: query-driven worker scale above one replica.
Resource            resources/local.resource
Test Tags           local    k3d    scaleout    trino    keda
Test Teardown       Run Keyword If Test Failed    Dump Debug

*** Variables ***
${SCALEOUT_MAX_WORKERS}      2
${SCALEOUT_THRESHOLD}        0.5
${SCALEOUT_WAIT_SECONDS}     420
${SCALEOUT_QUERY}            SELECT count(*) FROM "local-tpch".sf1000.lineitem WHERE rand() >= 0

*** Test Cases ***
Real Trino Query Metrics Drive Worker Scale Out
    File Should Exist    ${SMOKE_SCRIPT}
    ${result}=    Run Process    ${SMOKE_SCRIPT}
    ...    env:NAMESPACE=${NAMESPACE}
    ...    env:XTRINODE_NAME=${XTRINODE_NAME}
    ...    env:OPERATOR_NAMESPACE=${OPERATOR_NAMESPACE}
    ...    env:GATEWAY_NAMESPACE=${GATEWAY_NAMESPACE}
    ...    env:API_SERVER_NAMESPACE=${API_SERVER_NAMESPACE}
    ...    env:TRINO_IMAGE_REPOSITORY=${TRINO_IMAGE_REPOSITORY}
    ...    env:TRINO_IMAGE_TAG=${TRINO_IMAGE_TAG}
    ...    env:SCALEOUT_ENABLED=true
    ...    env:SCALEOUT_MAX_WORKERS=${SCALEOUT_MAX_WORKERS}
    ...    env:SCALEOUT_THRESHOLD=${SCALEOUT_THRESHOLD}
    ...    env:SCALEOUT_WAIT_SECONDS=${SCALEOUT_WAIT_SECONDS}
    ...    env:SCALEOUT_QUERY=${SCALEOUT_QUERY}
    ...    stderr=STDOUT
    Log    ${result.stdout}
    Should Be Equal As Integers    ${result.rc}    0    msg=${result.stdout}
