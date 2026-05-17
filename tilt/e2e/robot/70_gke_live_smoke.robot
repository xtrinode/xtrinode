*** Settings ***
Documentation       Opt-in live GKE smoke wrapper for the real operator, KEDA, gateway, and worker scaling path.
Resource            resources/local.resource
Suite Setup         Skip Unless GKE Live E2E Enabled
Test Teardown       Run Keyword If Test Failed    Dump Debug
Test Tags           gke    live    smoke    keda    lifecycle

*** Test Cases ***
GKE KEDA Resume Smoke Passes
    ${result}=    Run Process    ${REPO_ROOT}/scripts/smoke/gcp-keda-resume-smoke.sh    cwd=${REPO_ROOT}    stderr=STDOUT
    Log    ${result.stdout}
    Should Be Equal As Integers    ${result.rc}    0    msg=${result.stdout}

*** Keywords ***
Skip Unless GKE Live E2E Enabled
    ${enabled}=    Get Environment Variable    GKE_E2E_ENABLED    false
    Skip If    '${enabled}' != 'true'    GKE live smoke is opt-in; set GKE_E2E_ENABLED=true after selecting the target kubectl context.
