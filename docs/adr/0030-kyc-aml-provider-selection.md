---
name: adr-030-kyc-aml-provider-selection
description: Select Onfido as the primary KYC/AML verification provider for identity verification and sanctions screening
triggers:
  - Choosing which KYC/AML provider to integrate first
  - Evaluating vendor trade-offs for identity verification
  - Planning compliance coverage for UK/EU jurisdictions

instructions: |
  Use Onfido as the primary KYC/AML provider for identity verification, document
  checks, and sanctions screening. Integrate via their RESTful API with webhook-based
  async verification flows. Refer to ADR-021 for the provider abstraction architecture
  that enables future multi-provider support.
---

# 30. KYC/AML Provider Selection

Date: 2026-02-12

## Status

Accepted

## Context

Meridian requires an external KYC/AML provider to perform identity verification, document checks, and sanctions screening for the Party Directory service (see [ADR-021](./0021-kyc-aml-verification-provider-architecture.md) for the integration architecture). The provider abstraction layer defined in ADR-021 supports multiple providers, but an initial provider must be selected for the first implementation phase.

The selection must balance several competing concerns:

* **Regulatory coverage**: UK and EU compliance (AMLD5, GDPR, PSD2) is the immediate priority, with global expansion planned
* **Integration quality**: RESTful APIs, sandbox environments, and webhook support reduce development effort
* **Sanctions screening**: Must support real-time screening against global sanctions lists (OFAC, EU, UN, HMT)
* **Cost predictability**: Per-check pricing aligns with Meridian's multi-tenant billing model
* **Ecosystem maturity**: Provider stability, documentation quality, and community adoption reduce integration risk

## Decision Drivers

* RESTful API with sandbox environment for development and testing
* Webhook support for async verification flows (required by ADR-021)
* Global sanctions and PEP screening capability
* Strong UK/EU geographic coverage and compliance certifications
* Transparent per-check pricing model
* Go SDK availability (preferred, not required)
* Provider stability and market track record

## Considered Options

1. **Onfido** - Identity verification and sanctions screening platform
2. **Jumio** - Document and biometric verification platform
3. **Sumsub** - All-in-one compliance platform
4. **Sardine** - Fraud prevention and KYC platform

## Decision Outcome

Chosen option: **Onfido**, because it provides the strongest combination of API quality, UK/EU regulatory coverage, sanctions screening capability, and webhook-driven async flows that align with the architecture defined in ADR-021.

### Positive Consequences

* Well-documented RESTful API reduces integration effort
* Sandbox environment enables automated integration testing
* Webhook-based verification aligns directly with ADR-021 async-first architecture
* Strong UK/EU presence matches Meridian's initial geographic focus
* Integrated sanctions screening avoids needing a separate provider for AML checks
* Per-check pricing model maps cleanly to multi-tenant cost allocation

### Negative Consequences

* Higher per-check cost compared to Sumsub and Sardine
* No official Go SDK (HTTP client wrapper required)
* Single-provider dependency until a second provider is integrated via ADR-021 abstraction layer

## Provider Comparison

| Criterion | Onfido | Jumio | Sumsub | Sardine |
|-----------|--------|-------|--------|---------|
| **API style** | RESTful, well-documented | RESTful, more complex | RESTful | RESTful |
| **Sandbox environment** | Yes, full-featured | Yes | Yes | Limited |
| **Webhook support** | Yes, HMAC-SHA256 signed | Yes, IP allowlisting (no HMAC) | Yes | Yes |
| **Sanctions screening** | Integrated (OFAC, EU, UN, HMT) | Via partner (ComplyAdvantage) | Integrated | Limited |
| **PEP screening** | Integrated | Via partner | Integrated | Limited |
| **UK/EU compliance** | Strong (ETSI-certified, client base in regulated sectors, GDPR) | Good (EU presence) | Growing (London office, less established in UK) | Limited UK presence |
| **AMLD5 coverage** | Yes | Yes | Partial | Partial |
| **Go SDK** | No (community clients available) | No | No | No |
| **Pricing model** | Per-check | Per-check | Tiered/volume | Per-check |
| **Relative cost** | Higher | Higher | Lower | Lower |
| **Market maturity** | Established (2012, UK-headquartered) | Established (2010) | Growing (2015) | Early stage (2020) |
| **Identity verification** | Document + biometric | Document + biometric | Document + biometric | Document + biometric |
| **Manual review** | Supported | Supported | Supported | Limited |

## Pros and Cons of the Options

### Onfido (Chosen)

UK-headquartered identity verification platform with integrated sanctions screening. Used by financial institutions, fintechs, and regulated platforms across Europe.

* Good, because API documentation is thorough with clear examples and error codes
* Good, because sandbox environment supports full verification flow testing
* Good, because HMAC-signed webhooks align with ADR-021 webhook handler design
* Good, because integrated sanctions screening eliminates need for a separate AML provider
* Good, because UK headquarters, ETSI TS 119 461 certification, and client base in regulated sectors demonstrate strong UK/EU compliance posture
* Good, because per-check pricing maps to Meridian's multi-tenant cost allocation
* Bad, because per-check cost is higher than volume-based alternatives
* Bad, because no official Go SDK requires building an HTTP client wrapper

### Jumio

US-headquartered document and biometric verification platform. Strong in identity proofing with AI-powered document verification.

* Good, because document verification accuracy is industry-leading
* Good, because biometric verification includes advanced liveness detection
* Good, because established presence in both US and EU markets
* Bad, because sanctions screening requires a separate partner integration (ComplyAdvantage)
* Bad, because API integration is more complex with more configuration surface area
* Bad, because requiring a separate AML provider increases integration scope and vendor management

### Sumsub

All-in-one compliance platform offering KYC, AML, and fraud prevention in a single product.

* Good, because single platform covers identity, AML, and fraud detection
* Good, because volume-based pricing is more cost-effective at scale
* Good, because flexible workflow builder allows custom verification flows
* Bad, because less established in UK market compared to Onfido
* Bad, because AMLD5 coverage is partial, requiring validation for specific jurisdictions
* Bad, because newer platform with less track record in regulated financial services

### Sardine

Fraud prevention platform with KYC capabilities, focused on real-time risk scoring using device and behavioral signals.

* Good, because real-time risk scoring provides fraud prevention alongside identity verification
* Good, because device intelligence adds a layer beyond document verification
* Bad, because limited sanctions and PEP screening capability
* Bad, because limited presence and compliance track record in UK/EU markets
* Bad, because newer entrant (founded 2020) with less regulatory validation
* Bad, because primary focus is fraud prevention rather than regulatory KYC/AML compliance

## Links

* [ADR-021: KYC/AML Verification Provider Architecture](./0021-kyc-aml-verification-provider-architecture.md)
* [ADR-005: Adapter Pattern for Layer Translation](./0005-adapter-pattern-layer-translation.md)
* [ADR-019: Resilient Client Patterns](./0019-resilient-client-patterns.md)

## Notes

### Future Provider Additions

The provider abstraction layer (ADR-021) supports adding providers without code changes to the orchestration layer. When expanding geographic coverage:

* **US market**: Evaluate Jumio or Persona as the US-focused provider
* **Global sanctions**: Consider ComplyAdvantage as a dedicated sanctions screening provider if Onfido's coverage proves insufficient
* **Cost optimisation**: Re-evaluate Sumsub for high-volume tenants where per-check costs become significant

### Re-evaluation Triggers

Revisit this decision if:

* Onfido's sanctions screening coverage gaps emerge for required jurisdictions
* Per-check costs exceed budget thresholds at projected verification volumes
* A provider releases an official Go SDK that significantly reduces integration effort
* Regulatory requirements mandate a provider with specific certifications not held by Onfido
