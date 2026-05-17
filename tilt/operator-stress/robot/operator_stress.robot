*** Settings ***
Documentation       Local k3d operator reconcile stress for lightweight suspended runtimes and catalog churn.
Resource            ../../e2e/robot/resources/local.resource
Suite Setup         Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    Deployment Should Be Available    ${OPERATOR_NAMESPACE}    xtrinode-operator    1
Test Tags           local    k3d    stress    operator
Test Teardown       Run Keywords    Run Keyword If Test Failed    Dump Operator Stress Debug    AND    Cleanup Operator Stress Resources

*** Variables ***
${OPERATOR_STRESS_SCRIPT}                         ${REPO_ROOT}/tilt/e2e/helpers/local-operator-stress.sh
${OPERATOR_STRESS_NAMESPACE}                      team-operator-stress
${OPERATOR_STRESS_COUNT}                          12
${OPERATOR_STRESS_PATCH_ROUNDS}                   3
${OPERATOR_STRESS_WAIT_TIMEOUT_SECONDS}           240
${OPERATOR_STRESS_MAX_RECONCILE_ERROR_DELTA}      0
${OPERATOR_STRESS_METRICS_PORT}                   18082

*** Test Cases ***
Operator Handles Suspended Runtime And Catalog Churn
    File Should Exist    ${OPERATOR_STRESS_SCRIPT}
    ${result}=    Run Process    bash    ${OPERATOR_STRESS_SCRIPT}
    ...    env:NAMESPACE=${OPERATOR_STRESS_NAMESPACE}
    ...    env:OPERATOR_NAMESPACE=${OPERATOR_NAMESPACE}
    ...    env:GATEWAY_NAMESPACE=${GATEWAY_NAMESPACE}
    ...    env:OPERATOR_STRESS_COUNT=${OPERATOR_STRESS_COUNT}
    ...    env:OPERATOR_STRESS_PATCH_ROUNDS=${OPERATOR_STRESS_PATCH_ROUNDS}
    ...    env:OPERATOR_STRESS_WAIT_TIMEOUT_SECONDS=${OPERATOR_STRESS_WAIT_TIMEOUT_SECONDS}
    ...    env:OPERATOR_STRESS_MAX_RECONCILE_ERROR_DELTA=${OPERATOR_STRESS_MAX_RECONCILE_ERROR_DELTA}
    ...    env:OPERATOR_STRESS_METRICS_PORT=${OPERATOR_STRESS_METRICS_PORT}
    ...    stderr=STDOUT
    Log    ${result.stdout}
    Should Be Equal As Integers    ${result.rc}    0    msg=${result.stdout}

*** Keywords ***
Cleanup Operator Stress Resources
    ${result}=    Run Process    bash    ${OPERATOR_STRESS_SCRIPT}    cleanup
    ...    env:NAMESPACE=${OPERATOR_STRESS_NAMESPACE}
    ...    env:OPERATOR_NAMESPACE=${OPERATOR_NAMESPACE}
    ...    env:GATEWAY_NAMESPACE=${GATEWAY_NAMESPACE}
    ...    stderr=STDOUT
    Log    ${result.stdout}

Dump Operator Stress Debug
    ${result}=    Run Process    bash    ${OPERATOR_STRESS_SCRIPT}    debug
    ...    env:NAMESPACE=${OPERATOR_STRESS_NAMESPACE}
    ...    env:OPERATOR_NAMESPACE=${OPERATOR_NAMESPACE}
    ...    env:GATEWAY_NAMESPACE=${GATEWAY_NAMESPACE}
    ...    stderr=STDOUT
    Log    ${result.stdout}
