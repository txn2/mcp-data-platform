import type React from "react";
import { ConnectionAlertCard } from "./ConnectionAlertCard";
import { MapsCard } from "./MapsCard";
import { SitesSettingsCard } from "./SitesSettingsCard";
import { NotificationChannelsCard } from "./NotificationChannelsCard";
import { ReviewAlertCard } from "./ReviewAlertCard";
import { GeneralFeedbackCard } from "./GeneralFeedbackCard";
import { ScriptRunAlertCard } from "./ScriptRunAlertCard";
import { BotChecksSettingsCard } from "./BotChecksSettingsCard";
import { AccessRequestsSettingsCard } from "./AccessRequestsSettingsCard";
import { AppDBAlertCard } from "./AppDBAlertCard";
import { EmbedHostsSettingsCard } from "./EmbedHostsSettingsCard";
import { StorageSettingsCard } from "./StorageSettingsCard";
import { WebhookAlertCard } from "./WebhookAlertCard";
import { IntegrationsCard } from "./IntegrationsCard";
import type { SettingsSectionId } from "./settingsSections";

// OTHER_SECTION_CARDS is the card every settings section but Email shows
// (Email's card lives with the page). A new section is one entry here and one
// in SETTINGS_SECTIONS.
export const OTHER_SECTION_CARDS: Record<Exclude<SettingsSectionId, "email">, (isReadOnly: boolean) => React.ReactNode> = {
  channels: (ro) => <NotificationChannelsCard isReadOnly={ro} />,
  "general-feedback": (ro) => <GeneralFeedbackCard isReadOnly={ro} />,
  "knowledge-review": (ro) => (
    <ReviewAlertCard
      queue="review-queue-alert"
      title="Knowledge review queue alert"
      description="Email a digest when knowledge insights are left unreviewed"
      enabledHelp="Check the pending insight queue hourly and alert when it crosses a threshold"
      itemNoun="insight"
      isReadOnly={ro}
    />
  ),
  "approvals-queue": (ro) => (
    <ReviewAlertCard
      queue="approval-queue-alert"
      title="Approvals queue alert"
      description="Email when held calls and handler versions wait for a decision"
      enabledHelp="Check the approvals queue hourly and alert when it crosses a threshold"
      itemNoun="request"
      isReadOnly={ro}
    />
  ),
  "connection-revocations": (ro) => <ConnectionAlertCard isReadOnly={ro} />,
  "run-failures": (ro) => <ScriptRunAlertCard isReadOnly={ro} />,
  webhooks: (ro) => <WebhookAlertCard isReadOnly={ro} />,
  storage: (ro) => <StorageSettingsCard isReadOnly={ro} />,
  "application-databases": (ro) => <AppDBAlertCard isReadOnly={ro} />,
  maps: () => <MapsCard />,
  sites: () => <SitesSettingsCard />,
  "embed-hosts": (ro) => <EmbedHostsSettingsCard isReadOnly={ro} />,
  "bot-checks": (ro) => <BotChecksSettingsCard isReadOnly={ro} />,
  "access-requests": (ro) => <AccessRequestsSettingsCard isReadOnly={ro} />,
  integrations: (ro) => <IntegrationsCard isReadOnly={ro} />,
};
