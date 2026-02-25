import { createClient } from '@connectrpc/connect'
import type { Transport } from '@connectrpc/connect'
import { CurrentAccountService } from './gen/meridian/current_account/v1/current_account_pb'
import { PaymentOrderService } from './gen/meridian/payment_order/v1/payment_order_pb'
import { FinancialAccountingService } from './gen/meridian/financial_accounting/v1/financial_accounting_pb'
import { PositionKeepingService } from './gen/meridian/position_keeping/v1/position_keeping_pb'
import { AccountReconciliationService } from './gen/meridian/reconciliation/v1/reconciliation_pb'
import { PartyService } from './gen/meridian/party/v1/party_pb'
import { TenantService } from './gen/meridian/tenant/v1/tenant_pb'
import { SagaRegistryService } from './gen/meridian/saga/v1/saga_registry_pb'
import { SagaAdminService } from './gen/meridian/saga/v1/saga_admin_pb'
import { ReferenceDataService } from './gen/meridian/reference_data/v1/instrument_pb'
import { AccountTypeRegistryService } from './gen/meridian/reference_data/v1/account_type_pb'
import { NodeService } from './gen/meridian/reference_data/v1/node_pb'
import { InternalAccountService } from './gen/meridian/internal_account/v1/internal_account_pb'
import { MarketInformationService } from './gen/meridian/market_information/v1/market_information_pb'
import { MappingService } from './gen/meridian/mapping/v1/mapping_pb'
import { ForecastingService } from './gen/meridian/forecasting/v1/forecasting_pb'
import { ManifestHistoryService } from './gen/meridian/control_plane/v1/manifest_history_service_pb'
import { ApplyManifestService } from './gen/meridian/control_plane/v1/apply_manifest_service_pb'

export function createServiceClients(transport: Transport) {
  return {
    currentAccount: createClient(CurrentAccountService, transport),
    paymentOrder: createClient(PaymentOrderService, transport),
    financialAccounting: createClient(FinancialAccountingService, transport),
    positionKeeping: createClient(PositionKeepingService, transport),
    accountReconciliation: createClient(AccountReconciliationService, transport),
    party: createClient(PartyService, transport),
    tenant: createClient(TenantService, transport),
    sagaRegistry: createClient(SagaRegistryService, transport),
    sagaAdmin: createClient(SagaAdminService, transport),
    referenceData: createClient(ReferenceDataService, transport),
    accountTypeRegistry: createClient(AccountTypeRegistryService, transport),
    node: createClient(NodeService, transport),
    internalAccount: createClient(InternalAccountService, transport),
    marketInformation: createClient(MarketInformationService, transport),
    mapping: createClient(MappingService, transport),
    forecasting: createClient(ForecastingService, transport),
    manifestHistory: createClient(ManifestHistoryService, transport),
    manifestApplier: createClient(ApplyManifestService, transport),
  }
}

export type ServiceClients = ReturnType<typeof createServiceClients>
