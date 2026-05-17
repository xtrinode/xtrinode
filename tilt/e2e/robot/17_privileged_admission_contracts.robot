*** Settings ***
Documentation       Live admission contracts for privileged XTrinode fields.
Resource            resources/local.resource
Suite Setup         Ensure Privileged Admission Contract Prerequisites
Suite Teardown      Cleanup Privileged Admission Contract Objects
Test Tags           local    k3d    contracts    admission    xtrinode
Test Teardown       Run Keyword If Test Failed    Dump Privileged Admission Debug

*** Variables ***
${PRIVILEGED_NAMESPACE}       team-privileged-admission
${TENANT_SA}                  xtrinode-e2e-tenant
${PLATFORM_SA}                xtrinode-e2e-platform
${TENANT_USER}                system:serviceaccount:${PRIVILEGED_NAMESPACE}:${TENANT_SA}
${PLATFORM_USER}              system:serviceaccount:${PRIVILEGED_NAMESPACE}:${PLATFORM_SA}
${PLATFORM_BASE_XTRINODE}     platform-base
${PLATFORM_OVERLAY_XTRINODE}  platform-overlay
${TENANT_OVERLAY_XTRINODE}    tenant-overlay
${TENANT_HELM_XTRINODE}       tenant-helm
${TENANT_HELM_POLICY_XTRINODE}    tenant-helm-policy
${TLS_UNSUPPORTED_XTRINODE}   tls-unsupported
${JWT_UNSUPPORTED_XTRINODE}   jwt-unsupported
${HTTP_DISABLED_XTRINODE}     http-disabled
${HTTP_PORT_XTRINODE}         http-port-raw
${CATALOG_SECRET_NAME}        catalog-secret-target
${TENANT_CATALOG_SECRET_REF}  tenant-secret-ref
${PLATFORM_CATALOG_SECRET_REF}    platform-secret-ref
${TENANT_CATALOG_PROPERTY_SECRET_REF}  tenant-property-secret-ref
${PLATFORM_CATALOG_PROPERTY_SECRET_REF}    platform-property-secret-ref
${CATALOG_PLAINTEXT_PASSWORD}     plaintext-password-catalog
${CUSTOM_PLAINTEXT_SECRET}        plaintext-secret-catalog
${CATALOG_GENERATED_PROPERTY_COLLISION}    generated-property-collision
${CATALOG_GENERATED_SECRET_COLLISION}      generated-secret-collision

*** Test Cases ***
Tenant Cannot Create Privileged Overlay Fields
    ${can_create}=    Kubectl Output    auth    can-i    create    xtrinodes.analytics.xtrinode.io    -n    ${PRIVILEGED_NAMESPACE}    --as=${TENANT_USER}
    Should Be Equal    ${can_create}    yes
    ${overlay_manifest}=    Create Values Overlay Admission Manifest    ${TENANT_OVERLAY_XTRINODE}
    Admission Apply As User Should Fail    ${TENANT_USER}    ${overlay_manifest}    spec.valuesOverlay
    ${helm_manifest}=    Create Helm Chart Config Admission Manifest    ${TENANT_HELM_XTRINODE}
    Admission Apply As User Should Fail    ${TENANT_USER}    ${helm_manifest}    spec.helmChartConfig
    ${policy_manifest}=    Create Helm Policy Exposure Admission Manifest    ${TENANT_HELM_POLICY_XTRINODE}
    Admission Apply As User Should Fail    ${TENANT_USER}    ${policy_manifest}    spec.helmChartConfig

Tenant Cannot Add Privileged Overlay Fields On Update
    ${base_manifest}=    Create Base Admission Manifest    ${PLATFORM_BASE_XTRINODE}
    Command Should Succeed    kubectl    --as=${PLATFORM_USER}    apply    -f    ${base_manifest}
    ${patch}=    Set Variable    {"spec":{"valuesOverlay":{"image":{"repository":"${TRINO_IMAGE_REPOSITORY}","tag":"${TRINO_IMAGE_TAG}"}}}}
    ${result}=    Run Command Allow Failure    kubectl    --as=${TENANT_USER}    patch    xtrinode/${PLATFORM_BASE_XTRINODE}    -n    ${PRIVILEGED_NAMESPACE}    --type=merge    -p    ${patch}
    Should Not Be Equal As Integers    ${result.rc}    0
    Should Contain    ${result.stdout}    privileged fields
    Should Contain    ${result.stdout}    spec.valuesOverlay

Platform User Can Create Privileged Overlay Fields
    ${overlay_manifest}=    Create Values Overlay Admission Manifest    ${PLATFORM_OVERLAY_XTRINODE}
    Command Should Succeed    kubectl    --as=${PLATFORM_USER}    apply    -f    ${overlay_manifest}
    ${overlay}=    Kubectl Output    get    xtrinode/${PLATFORM_OVERLAY_XTRINODE}    -n    ${PRIVILEGED_NAMESPACE}    -o    jsonpath={.spec.valuesOverlay.image.tag}
    Should Be Equal    ${overlay}    ${TRINO_IMAGE_TAG}

Platform User Cannot Create Unsupported Trino Control Modes
    ${tls_manifest}=    Create TLS Server Admission Manifest    ${TLS_UNSUPPORTED_XTRINODE}
    Admission Apply As User Should Fail With Message    ${PLATFORM_USER}    ${tls_manifest}    Trino TLS server mode disables HTTP
    ${jwt_manifest}=    Create Unsupported JWT Admission Manifest    ${JWT_UNSUPPORTED_XTRINODE}
    Admission Apply As User Should Fail With Message    ${PLATFORM_USER}    ${jwt_manifest}    Unsupported value: "JWT"
    ${disabled_manifest}=    Create HTTP Disabled Admission Manifest    ${HTTP_DISABLED_XTRINODE}
    Admission Apply As User Should Fail With Message    ${PLATFORM_USER}    ${disabled_manifest}    HTTP listener must stay enabled
    ${port_manifest}=    Create Raw HTTP Port Admission Manifest    ${HTTP_PORT_XTRINODE}
    Admission Apply As User Should Fail With Message    ${PLATFORM_USER}    ${port_manifest}    valuesOverlay.service.port

Tenant Cannot Create Catalog Secret Reference Without Secret Get
    ${tenant_manifest}=    Create Postgres Catalog Secret Reference Manifest    ${TENANT_CATALOG_SECRET_REF}
    Admission Apply As User Should Fail With Message    ${TENANT_USER}    ${tenant_manifest}    requires get permission on secrets
    ${platform_manifest}=    Create Postgres Catalog Secret Reference Manifest    ${PLATFORM_CATALOG_SECRET_REF}
    Command Should Succeed    kubectl    --as=${PLATFORM_USER}    apply    -f    ${platform_manifest}
    ${tenant_property_manifest}=    Create Cassandra Catalog Property Secret Reference Manifest    ${TENANT_CATALOG_PROPERTY_SECRET_REF}
    Admission Apply As User Should Fail With Message    ${TENANT_USER}    ${tenant_property_manifest}    requires get permission on secrets
    ${platform_property_manifest}=    Create Cassandra Catalog Property Secret Reference Manifest    ${PLATFORM_CATALOG_PROPERTY_SECRET_REF}
    Command Should Succeed    kubectl    --as=${PLATFORM_USER}    apply    -f    ${platform_property_manifest}

Platform User Cannot Create Catalog Plaintext Secret Properties
    ${jdbc_manifest}=    Create Postgres Catalog Plaintext Password Manifest    ${CATALOG_PLAINTEXT_PASSWORD}
    Admission Apply As User Should Fail With Message    ${PLATFORM_USER}    ${jdbc_manifest}    use connectionPasswordSecret
    ${custom_manifest}=    Create Custom Catalog Plaintext Secret Manifest    ${CUSTOM_PLAINTEXT_SECRET}
    Admission Apply As User Should Fail With Message    ${PLATFORM_USER}    ${custom_manifest}    sensitive catalog properties

Platform User Cannot Create Catalog Generated Property Collisions
    ${property_manifest}=    Create Cassandra Catalog Generated Property Collision Manifest    ${CATALOG_GENERATED_PROPERTY_COLLISION}
    Admission Apply As User Should Fail With Message    ${PLATFORM_USER}    ${property_manifest}    connector.name is generated
    Admission Apply As User Should Fail With Message    ${PLATFORM_USER}    ${property_manifest}    typed connector fields
    ${secret_ref_manifest}=    Create MySQL Catalog Generated Secret Reference Collision Manifest    ${CATALOG_GENERATED_SECRET_COLLISION}
    Admission Apply As User Should Fail With Message    ${PLATFORM_USER}    ${secret_ref_manifest}    connector.name is generated
    Admission Apply As User Should Fail With Message    ${PLATFORM_USER}    ${secret_ref_manifest}    typed connector fields

*** Keywords ***
Ensure Privileged Admission Contract Prerequisites
    Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    Deployment Should Be Available    ${OPERATOR_NAMESPACE}    xtrinode-operator    1
    Create Privileged Admission Namespace If Missing
    Ensure Privileged Admission Catalog Secret
    Apply Privileged Admission RBAC
    Cleanup Privileged Admission XTrinodes
    Cleanup Privileged Admission Catalogs

Create Privileged Admission Namespace If Missing
    ${result}=    Run Process    kubectl    create    namespace    ${PRIVILEGED_NAMESPACE}    stderr=STDOUT
    Log    ${result.stdout}
    IF    ${result.rc} != 0
        Should Contain    ${result.stdout}    AlreadyExists
    END

Apply Privileged Admission RBAC
    ${manifest}=    Set Variable    /tmp/xtrinode-privileged-admission-rbac.json
    ${json}=    Set Variable    {"apiVersion":"v1","kind":"List","items":[{"apiVersion":"v1","kind":"ServiceAccount","metadata":{"name":"${TENANT_SA}","namespace":"${PRIVILEGED_NAMESPACE}"}},{"apiVersion":"v1","kind":"ServiceAccount","metadata":{"name":"${PLATFORM_SA}","namespace":"${PRIVILEGED_NAMESPACE}"}},{"apiVersion":"rbac.authorization.k8s.io/v1","kind":"Role","metadata":{"name":"xtrinode-e2e-editor","namespace":"${PRIVILEGED_NAMESPACE}"},"rules":[{"apiGroups":["analytics.xtrinode.io"],"resources":["xtrinodes","xtrinodecatalogs"],"verbs":["get","list","watch","create","update","patch","delete"]}]},{"apiVersion":"rbac.authorization.k8s.io/v1","kind":"Role","metadata":{"name":"xtrinode-e2e-status-updater","namespace":"${PRIVILEGED_NAMESPACE}"},"rules":[{"apiGroups":["analytics.xtrinode.io"],"resources":["xtrinodes/status"],"verbs":["get","update","patch"]}]},{"apiVersion":"rbac.authorization.k8s.io/v1","kind":"Role","metadata":{"name":"xtrinode-e2e-secret-reader","namespace":"${PRIVILEGED_NAMESPACE}"},"rules":[{"apiGroups":[""],"resources":["secrets"],"resourceNames":["${CATALOG_SECRET_NAME}"],"verbs":["get"]}]},{"apiVersion":"rbac.authorization.k8s.io/v1","kind":"RoleBinding","metadata":{"name":"xtrinode-e2e-tenant-editor","namespace":"${PRIVILEGED_NAMESPACE}"},"roleRef":{"apiGroup":"rbac.authorization.k8s.io","kind":"Role","name":"xtrinode-e2e-editor"},"subjects":[{"kind":"ServiceAccount","name":"${TENANT_SA}","namespace":"${PRIVILEGED_NAMESPACE}"}]},{"apiVersion":"rbac.authorization.k8s.io/v1","kind":"RoleBinding","metadata":{"name":"xtrinode-e2e-platform-editor","namespace":"${PRIVILEGED_NAMESPACE}"},"roleRef":{"apiGroup":"rbac.authorization.k8s.io","kind":"Role","name":"xtrinode-e2e-editor"},"subjects":[{"kind":"ServiceAccount","name":"${PLATFORM_SA}","namespace":"${PRIVILEGED_NAMESPACE}"}]},{"apiVersion":"rbac.authorization.k8s.io/v1","kind":"RoleBinding","metadata":{"name":"xtrinode-e2e-platform-status","namespace":"${PRIVILEGED_NAMESPACE}"},"roleRef":{"apiGroup":"rbac.authorization.k8s.io","kind":"Role","name":"xtrinode-e2e-status-updater"},"subjects":[{"kind":"ServiceAccount","name":"${PLATFORM_SA}","namespace":"${PRIVILEGED_NAMESPACE}"}]},{"apiVersion":"rbac.authorization.k8s.io/v1","kind":"RoleBinding","metadata":{"name":"xtrinode-e2e-platform-secret-reader","namespace":"${PRIVILEGED_NAMESPACE}"},"roleRef":{"apiGroup":"rbac.authorization.k8s.io","kind":"Role","name":"xtrinode-e2e-secret-reader"},"subjects":[{"kind":"ServiceAccount","name":"${PLATFORM_SA}","namespace":"${PRIVILEGED_NAMESPACE}"}]}]}
    Create File    ${manifest}    ${json}
    Command Should Succeed    kubectl    apply    -f    ${manifest}

Ensure Privileged Admission Catalog Secret
    ${secret_yaml}=    Command Should Succeed    kubectl    create    secret    generic    ${CATALOG_SECRET_NAME}    -n    ${PRIVILEGED_NAMESPACE}    --from-literal=password=secret    --dry-run=client    -o    yaml
    ${manifest}=    Set Variable    /tmp/xtrinode-privileged-admission-catalog-secret.yaml
    Create File    ${manifest}    ${secret_yaml}
    Command Should Succeed    kubectl    apply    -f    ${manifest}

Create Base Admission Manifest
    [Arguments]    ${name}
    ${manifest}=    Set Variable    /tmp/xtrinode-privileged-admission-${name}.json
    ${json}=    Set Variable    {"apiVersion":"analytics.xtrinode.io/v1","kind":"XTrinode","metadata":{"name":"${name}","namespace":"${PRIVILEGED_NAMESPACE}","labels":{"test.xtrinode.io/contract":"privileged-admission"}},"spec":{"size":"xs","minWorkers":0,"maxWorkers":1,"suspended":true,"routing":{"header":"X-Trino-XTrinode=${PRIVILEGED_NAMESPACE}/${name}","routingGroup":"${name}"}}}
    Create File    ${manifest}    ${json}
    RETURN    ${manifest}

Create Values Overlay Admission Manifest
    [Arguments]    ${name}
    ${manifest}=    Set Variable    /tmp/xtrinode-privileged-admission-${name}.json
    ${json}=    Set Variable    {"apiVersion":"analytics.xtrinode.io/v1","kind":"XTrinode","metadata":{"name":"${name}","namespace":"${PRIVILEGED_NAMESPACE}","labels":{"test.xtrinode.io/contract":"privileged-admission"}},"spec":{"size":"xs","minWorkers":0,"maxWorkers":1,"suspended":true,"routing":{"header":"X-Trino-XTrinode=${PRIVILEGED_NAMESPACE}/${name}","routingGroup":"${name}"},"valuesOverlay":{"image":{"repository":"${TRINO_IMAGE_REPOSITORY}","tag":"${TRINO_IMAGE_TAG}","pullPolicy":"IfNotPresent"}}}}
    Create File    ${manifest}    ${json}
    RETURN    ${manifest}

Create Helm Chart Config Admission Manifest
    [Arguments]    ${name}
    ${manifest}=    Set Variable    /tmp/xtrinode-privileged-admission-${name}.json
    ${json}=    Set Variable    {"apiVersion":"analytics.xtrinode.io/v1","kind":"XTrinode","metadata":{"name":"${name}","namespace":"${PRIVILEGED_NAMESPACE}","labels":{"test.xtrinode.io/contract":"privileged-admission"}},"spec":{"size":"xs","minWorkers":0,"maxWorkers":1,"suspended":true,"routing":{"header":"X-Trino-XTrinode=${PRIVILEGED_NAMESPACE}/${name}","routingGroup":"${name}"},"helmChartConfig":{"worker":{"gracefulShutdown":{"enabled":true,"gracePeriodSeconds":5}}}}}
    Create File    ${manifest}    ${json}
    RETURN    ${manifest}

Create Helm Policy Exposure Admission Manifest
    [Arguments]    ${name}
    ${manifest}=    Set Variable    /tmp/xtrinode-privileged-admission-${name}.json
    ${json}=    Set Variable    {"apiVersion":"analytics.xtrinode.io/v1","kind":"XTrinode","metadata":{"name":"${name}","namespace":"${PRIVILEGED_NAMESPACE}","labels":{"test.xtrinode.io/contract":"privileged-admission"}},"spec":{"size":"xs","minWorkers":0,"maxWorkers":1,"suspended":true,"routing":{"header":"X-Trino-XTrinode=${PRIVILEGED_NAMESPACE}/${name}","routingGroup":"${name}"},"helmChartConfig":{"accessControl":{"type":"properties","properties":"access-control.name=allow-all"},"ingress":{"enabled":true,"hosts":[{"host":"${name}.example.test","paths":[{"path":"/","pathType":"Prefix"}]}]},"networkPolicy":{"enabled":true},"serviceMonitor":{"enabled":true,"interval":"30s"}}}}
    Create File    ${manifest}    ${json}
    RETURN    ${manifest}

Create TLS Server Admission Manifest
    [Arguments]    ${name}
    ${manifest}=    Set Variable    /tmp/xtrinode-privileged-admission-${name}.json
    ${json}=    Set Variable    {"apiVersion":"analytics.xtrinode.io/v1","kind":"XTrinode","metadata":{"name":"${name}","namespace":"${PRIVILEGED_NAMESPACE}","labels":{"test.xtrinode.io/contract":"privileged-admission"}},"spec":{"size":"xs","minWorkers":0,"maxWorkers":1,"suspended":true,"tls":{"serverSecretClass":"server-tls","internalSecretClass":"internal-tls"}}}
    Create File    ${manifest}    ${json}
    RETURN    ${manifest}

Create Unsupported JWT Admission Manifest
    [Arguments]    ${name}
    ${manifest}=    Set Variable    /tmp/xtrinode-privileged-admission-${name}.json
    ${json}=    Set Variable    {"apiVersion":"analytics.xtrinode.io/v1","kind":"XTrinode","metadata":{"name":"${name}","namespace":"${PRIVILEGED_NAMESPACE}","labels":{"test.xtrinode.io/contract":"privileged-admission"}},"spec":{"size":"xs","minWorkers":0,"maxWorkers":1,"suspended":true,"trinoControlAuth":{"username":"xtrinode-operator","passwordSecret":{"name":"trino-control-auth","key":"password"}},"valuesOverlay":{"additionalConfigProperties":["http-server.authentication.type=JWT"]}}}
    Create File    ${manifest}    ${json}
    RETURN    ${manifest}

Create HTTP Disabled Admission Manifest
    [Arguments]    ${name}
    ${manifest}=    Set Variable    /tmp/xtrinode-privileged-admission-${name}.json
    ${json}=    Set Variable    {"apiVersion":"analytics.xtrinode.io/v1","kind":"XTrinode","metadata":{"name":"${name}","namespace":"${PRIVILEGED_NAMESPACE}","labels":{"test.xtrinode.io/contract":"privileged-admission"}},"spec":{"size":"xs","minWorkers":0,"maxWorkers":1,"suspended":true,"valuesOverlay":{"additionalConfigProperties":["http-server.http.enabled=false"]}}}
    Create File    ${manifest}    ${json}
    RETURN    ${manifest}

Create Raw HTTP Port Admission Manifest
    [Arguments]    ${name}
    ${manifest}=    Set Variable    /tmp/xtrinode-privileged-admission-${name}.json
    ${json}=    Set Variable    {"apiVersion":"analytics.xtrinode.io/v1","kind":"XTrinode","metadata":{"name":"${name}","namespace":"${PRIVILEGED_NAMESPACE}","labels":{"test.xtrinode.io/contract":"privileged-admission"}},"spec":{"size":"xs","minWorkers":0,"maxWorkers":1,"suspended":true,"valuesOverlay":{"server":{"coordinatorExtraConfig":"http-server.http.port=8181"}}}}
    Create File    ${manifest}    ${json}
    RETURN    ${manifest}

Create Postgres Catalog Secret Reference Manifest
    [Arguments]    ${name}
    ${manifest}=    Set Variable    /tmp/xtrinode-privileged-admission-catalog-${name}.json
    ${json}=    Set Variable    {"apiVersion":"analytics.xtrinode.io/v1","kind":"XTrinodeCatalog","metadata":{"name":"${name}","namespace":"${PRIVILEGED_NAMESPACE}","labels":{"test.xtrinode.io/contract":"privileged-admission"}},"spec":{"connector":{"postgres":{"connectionURL":"jdbc:postgresql://postgres:5432/analytics","connectionPasswordSecret":{"name":"${CATALOG_SECRET_NAME}","key":"password"}}}}}
    Create File    ${manifest}    ${json}
    RETURN    ${manifest}

Create Cassandra Catalog Property Secret Reference Manifest
    [Arguments]    ${name}
    ${manifest}=    Set Variable    /tmp/xtrinode-privileged-admission-catalog-${name}.json
    ${json}=    Set Variable    {"apiVersion":"analytics.xtrinode.io/v1","kind":"XTrinodeCatalog","metadata":{"name":"${name}","namespace":"${PRIVILEGED_NAMESPACE}","labels":{"test.xtrinode.io/contract":"privileged-admission"}},"spec":{"connector":{"cassandra":{"contactPoints":"cassandra.default.svc.cluster.local","propertySecretRefs":{"cassandra.password":{"name":"${CATALOG_SECRET_NAME}","key":"password"}},"properties":{"cassandra.load-policy.use-dc-aware":"true"}}}}}
    Create File    ${manifest}    ${json}
    RETURN    ${manifest}

Create Postgres Catalog Plaintext Password Manifest
    [Arguments]    ${name}
    ${manifest}=    Set Variable    /tmp/xtrinode-privileged-admission-catalog-${name}.json
    ${json}=    Set Variable    {"apiVersion":"analytics.xtrinode.io/v1","kind":"XTrinodeCatalog","metadata":{"name":"${name}","namespace":"${PRIVILEGED_NAMESPACE}","labels":{"test.xtrinode.io/contract":"privileged-admission"}},"spec":{"connector":{"postgres":{"connectionURL":"jdbc:postgresql://postgres:5432/analytics","properties":{"connection-password":"plaintext","ssl":"true"}}}}}
    Create File    ${manifest}    ${json}
    RETURN    ${manifest}

Create Custom Catalog Plaintext Secret Manifest
    [Arguments]    ${name}
    ${manifest}=    Set Variable    /tmp/xtrinode-privileged-admission-catalog-${name}.json
    ${json}=    Set Variable    {"apiVersion":"analytics.xtrinode.io/v1","kind":"XTrinodeCatalog","metadata":{"name":"${name}","namespace":"${PRIVILEGED_NAMESPACE}","labels":{"test.xtrinode.io/contract":"privileged-admission"}},"spec":{"connector":{"custom":{"connectorName":"custom","properties":{"client.secret":"plaintext","safe.property":"ok"}}}}}
    Create File    ${manifest}    ${json}
    RETURN    ${manifest}

Create Cassandra Catalog Generated Property Collision Manifest
    [Arguments]    ${name}
    ${manifest}=    Set Variable    /tmp/xtrinode-privileged-admission-catalog-${name}.json
    ${json}=    Set Variable    {"apiVersion":"analytics.xtrinode.io/v1","kind":"XTrinodeCatalog","metadata":{"name":"${name}","namespace":"${PRIVILEGED_NAMESPACE}","labels":{"test.xtrinode.io/contract":"privileged-admission"}},"spec":{"connector":{"cassandra":{"contactPoints":"cassandra.default.svc.cluster.local","port":9042,"properties":{"connector.name":"memory","cassandra.contact-points":"other.default.svc.cluster.local","cassandra.native-protocol-port":"9142","cassandra.load-policy.use-dc-aware":"true"}}}}}
    Create File    ${manifest}    ${json}
    RETURN    ${manifest}

Create MySQL Catalog Generated Secret Reference Collision Manifest
    [Arguments]    ${name}
    ${manifest}=    Set Variable    /tmp/xtrinode-privileged-admission-catalog-${name}.json
    ${json}=    Set Variable    {"apiVersion":"analytics.xtrinode.io/v1","kind":"XTrinodeCatalog","metadata":{"name":"${name}","namespace":"${PRIVILEGED_NAMESPACE}","labels":{"test.xtrinode.io/contract":"privileged-admission"}},"spec":{"connector":{"mysql":{"connectionURL":"jdbc:mysql://mysql:3306/analytics","connectionPasswordSecret":{"name":"${CATALOG_SECRET_NAME}","key":"password"},"propertySecretRefs":{"connector.name":{"name":"${CATALOG_SECRET_NAME}","key":"password"},"connection-password":{"name":"${CATALOG_SECRET_NAME}","key":"password"}}}}}}
    Create File    ${manifest}    ${json}
    RETURN    ${manifest}

Admission Apply As User Should Fail
    [Arguments]    ${user}    ${manifest}    ${field}
    ${result}=    Run Command Allow Failure    kubectl    --as=${user}    apply    -f    ${manifest}
    Should Not Be Equal As Integers    ${result.rc}    0
    Should Contain    ${result.stdout}    privileged fields
    Should Contain    ${result.stdout}    ${field}

Admission Apply As User Should Fail With Message
    [Arguments]    ${user}    ${manifest}    ${message}
    ${result}=    Run Command Allow Failure    kubectl    --as=${user}    apply    --validate=false    -f    ${manifest}
    Should Not Be Equal As Integers    ${result.rc}    0
    Should Contain    ${result.stdout}    ${message}

Cleanup Privileged Admission Contract Objects
    Cleanup Privileged Admission XTrinodes
    Cleanup Privileged Admission Catalogs
    Run Command Allow Failure    kubectl    delete    rolebinding    xtrinode-e2e-tenant-editor    xtrinode-e2e-platform-editor    xtrinode-e2e-platform-status    xtrinode-e2e-platform-secret-reader    -n    ${PRIVILEGED_NAMESPACE}    --ignore-not-found=true
    Run Command Allow Failure    kubectl    delete    role    xtrinode-e2e-editor    xtrinode-e2e-status-updater    xtrinode-e2e-secret-reader    -n    ${PRIVILEGED_NAMESPACE}    --ignore-not-found=true
    Run Command Allow Failure    kubectl    delete    serviceaccount    ${TENANT_SA}    ${PLATFORM_SA}    -n    ${PRIVILEGED_NAMESPACE}    --ignore-not-found=true
    Run Command Allow Failure    kubectl    delete    secret    ${CATALOG_SECRET_NAME}    -n    ${PRIVILEGED_NAMESPACE}    --ignore-not-found=true

Cleanup Privileged Admission XTrinodes
    FOR    ${name}    IN    ${PLATFORM_BASE_XTRINODE}    ${PLATFORM_OVERLAY_XTRINODE}    ${TENANT_OVERLAY_XTRINODE}    ${TENANT_HELM_XTRINODE}    ${TENANT_HELM_POLICY_XTRINODE}    ${TLS_UNSUPPORTED_XTRINODE}    ${JWT_UNSUPPORTED_XTRINODE}    ${HTTP_DISABLED_XTRINODE}    ${HTTP_PORT_XTRINODE}
        Run Command Allow Failure    kubectl    patch    xtrinode/${name}    -n    ${PRIVILEGED_NAMESPACE}    --type=merge    -p    {"metadata":{"finalizers":[]}}
        Run Command Allow Failure    kubectl    delete    xtrinode/${name}    -n    ${PRIVILEGED_NAMESPACE}    --wait=false    --ignore-not-found=true
        Run Command Allow Failure    kubectl    wait    xtrinode/${name}    -n    ${PRIVILEGED_NAMESPACE}    --for=delete    --timeout=120s
        Run Command Allow Failure    kubectl    delete    deployment,service,configmap,poddisruptionbudget,serviceaccount,horizontalpodautoscaler,scaledobject,triggerauthentication    -n    ${PRIVILEGED_NAMESPACE}    -l    app.kubernetes.io/instance=${name}    --ignore-not-found=true    --wait=false
    END

Cleanup Privileged Admission Catalogs
    FOR    ${name}    IN    ${TENANT_CATALOG_SECRET_REF}    ${PLATFORM_CATALOG_SECRET_REF}    ${TENANT_CATALOG_PROPERTY_SECRET_REF}    ${PLATFORM_CATALOG_PROPERTY_SECRET_REF}    ${CATALOG_PLAINTEXT_PASSWORD}    ${CUSTOM_PLAINTEXT_SECRET}    ${CATALOG_GENERATED_PROPERTY_COLLISION}    ${CATALOG_GENERATED_SECRET_COLLISION}
        Run Command Allow Failure    kubectl    delete    xtrinodecatalog/${name}    -n    ${PRIVILEGED_NAMESPACE}    --wait=false    --ignore-not-found=true
    END

Dump Privileged Admission Debug
    Dump Debug
    Run Command Allow Failure    kubectl    get    serviceaccount,role,rolebinding,xtrinode,xtrinodecatalog,secret    -n    ${PRIVILEGED_NAMESPACE}    -o    yaml
    Run Command Allow Failure    kubectl    get    events    -n    ${PRIVILEGED_NAMESPACE}    --sort-by=.lastTimestamp
