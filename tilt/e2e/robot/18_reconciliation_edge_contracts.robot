*** Settings ***
Documentation       Live reconciliation edge contracts for manual drift and external mounted object changes.
Resource            resources/local.resource
Suite Setup         Ensure Local XTrinode Ready
Suite Teardown      Cleanup Reconciliation Edge Contracts
Test Tags           local    k3d    contracts    reconciliation
Test Teardown       Run Keyword If Test Failed    Dump Debug

*** Variables ***
${MOUNTED_CONFIG}       xtrinode-e2e-mounted-config
${MOUNTED_SECRET}       xtrinode-e2e-mounted-secret

*** Test Cases ***
Manual Coordinator Deployment Scale Drift Is Reconciled
    Command Should Succeed    kubectl    scale    deployment/trino-${XTRINODE_NAME}-coordinator    -n    ${NAMESPACE}    --replicas=0
    Wait Until Keyword Succeeds    180s    ${POLL_INTERVAL}    Deployment Spec Replicas Should Equal    ${NAMESPACE}    trino-${XTRINODE_NAME}-coordinator    1
    Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    Deployment Should Be Available    ${NAMESPACE}    trino-${XTRINODE_NAME}-coordinator    1

Manual Gateway Route ConfigMap Drift Is Reconciled
    ${patch}=    Set Variable    {"data":{"routes.yaml":"routes: []\\n"}}
    Command Should Succeed    kubectl    patch    configmap/trino-gateway-routes    -n    ${GATEWAY_NAMESPACE}    --type=merge    -p    ${patch}
    Wait Until Keyword Succeeds    180s    ${POLL_INTERVAL}    Gateway Route Should Be Registered

External Mounted ConfigMap And Secret Changes Roll Runtime
    Ensure External Mounted Resources
    Patch XTrinode With External Mounts
    Command Should Succeed    kubectl    rollout    status    deployment/trino-${XTRINODE_NAME}-coordinator    -n    ${NAMESPACE}    --timeout=300s
    Command Should Succeed    kubectl    rollout    status    deployment/trino-${XTRINODE_NAME}-worker    -n    ${NAMESPACE}    --timeout=300s

    ${coordinator_before_config}=    Kubectl Output    get    deployment    trino-${XTRINODE_NAME}-coordinator    -n    ${NAMESPACE}    -o    ${COORDINATOR_ROLLOUT_HASH_OUTPUT}
    ${worker_before_config}=    Kubectl Output    get    deployment    trino-${XTRINODE_NAME}-worker    -n    ${NAMESPACE}    -o    ${WORKER_ROLLOUT_HASH_OUTPUT}
    ${config_patch}=    Set Variable    {"data":{"settings.properties":"version=2"}}
    Command Should Succeed    kubectl    patch    configmap/${MOUNTED_CONFIG}    -n    ${NAMESPACE}    --type=merge    -p    ${config_patch}
    Wait Until Keyword Succeeds    180s    ${POLL_INTERVAL}    Deployment Pod Template Annotation Should Not Equal    ${NAMESPACE}    trino-${XTRINODE_NAME}-coordinator    ${COORDINATOR_ROLLOUT_HASH_OUTPUT}    ${coordinator_before_config}
    Wait Until Keyword Succeeds    180s    ${POLL_INTERVAL}    Deployment Pod Template Annotation Should Not Equal    ${NAMESPACE}    trino-${XTRINODE_NAME}-worker    ${WORKER_ROLLOUT_HASH_OUTPUT}    ${worker_before_config}
    Command Should Succeed    kubectl    rollout    status    deployment/trino-${XTRINODE_NAME}-coordinator    -n    ${NAMESPACE}    --timeout=300s
    Command Should Succeed    kubectl    rollout    status    deployment/trino-${XTRINODE_NAME}-worker    -n    ${NAMESPACE}    --timeout=300s

    ${coordinator_before_secret}=    Kubectl Output    get    deployment    trino-${XTRINODE_NAME}-coordinator    -n    ${NAMESPACE}    -o    ${COORDINATOR_ROLLOUT_HASH_OUTPUT}
    ${worker_before_secret}=    Kubectl Output    get    deployment    trino-${XTRINODE_NAME}-worker    -n    ${NAMESPACE}    -o    ${WORKER_ROLLOUT_HASH_OUTPUT}
    ${secret_patch}=    Set Variable    {"data":{"token":"c2Vjb25k"}}
    Command Should Succeed    kubectl    patch    secret/${MOUNTED_SECRET}    -n    ${NAMESPACE}    --type=merge    -p    ${secret_patch}
    Wait Until Keyword Succeeds    180s    ${POLL_INTERVAL}    Deployment Pod Template Annotation Should Not Equal    ${NAMESPACE}    trino-${XTRINODE_NAME}-coordinator    ${COORDINATOR_ROLLOUT_HASH_OUTPUT}    ${coordinator_before_secret}
    Wait Until Keyword Succeeds    180s    ${POLL_INTERVAL}    Deployment Pod Template Annotation Should Not Equal    ${NAMESPACE}    trino-${XTRINODE_NAME}-worker    ${WORKER_ROLLOUT_HASH_OUTPUT}    ${worker_before_secret}
    Command Should Succeed    kubectl    rollout    status    deployment/trino-${XTRINODE_NAME}-coordinator    -n    ${NAMESPACE}    --timeout=300s
    Command Should Succeed    kubectl    rollout    status    deployment/trino-${XTRINODE_NAME}-worker    -n    ${NAMESPACE}    --timeout=300s

*** Keywords ***
Ensure External Mounted Resources
    ${config_manifest}=    Command Should Succeed    kubectl    create    configmap    ${MOUNTED_CONFIG}    -n    ${NAMESPACE}    --from-literal=settings.properties=version=1    --dry-run=client    -o    yaml
    Create File    /tmp/xtrinode-e2e-mounted-config.yaml    ${config_manifest}
    Command Should Succeed    kubectl    apply    -f    /tmp/xtrinode-e2e-mounted-config.yaml
    ${secret_manifest}=    Command Should Succeed    kubectl    create    secret    generic    ${MOUNTED_SECRET}    -n    ${NAMESPACE}    --from-literal=token=first    --dry-run=client    -o    yaml
    Create File    /tmp/xtrinode-e2e-mounted-secret.yaml    ${secret_manifest}
    Command Should Succeed    kubectl    apply    -f    /tmp/xtrinode-e2e-mounted-secret.yaml

Patch XTrinode With External Mounts
    ${patch}=    Set Variable    {"spec":{"valuesOverlay":{"configMounts":[{"name":"${MOUNTED_CONFIG}","configMap":"${MOUNTED_CONFIG}","path":"/etc/trino/e2e-config"}],"secretMounts":[{"name":"${MOUNTED_SECRET}","secretName":"${MOUNTED_SECRET}","path":"/etc/trino/e2e-secret"}]}}}
    Command Should Succeed    kubectl    patch    xtrinode/${XTRINODE_NAME}    -n    ${NAMESPACE}    --type=merge    -p    ${patch}
    Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    Deployment Should Be Available    ${NAMESPACE}    trino-${XTRINODE_NAME}-coordinator    1
    Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    Deployment Should Be Available    ${NAMESPACE}    trino-${XTRINODE_NAME}-worker    1

Cleanup Reconciliation Edge Contracts
    ${patch}=    Set Variable    {"spec":{"valuesOverlay":{"configMounts":null,"secretMounts":null}}}
    Run Command Allow Failure    kubectl    patch    xtrinode/${XTRINODE_NAME}    -n    ${NAMESPACE}    --type=merge    -p    ${patch}
    Run Command Allow Failure    kubectl    delete    configmap/${MOUNTED_CONFIG}    -n    ${NAMESPACE}    --ignore-not-found=true
    Run Command Allow Failure    kubectl    delete    secret/${MOUNTED_SECRET}    -n    ${NAMESPACE}    --ignore-not-found=true
