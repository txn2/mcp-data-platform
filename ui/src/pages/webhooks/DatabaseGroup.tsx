import { useConnections } from "@/api/admin/hooks";
import { ConfigField, ConfigGroup, ConfigSelect } from "@/pages/settings/connections/fields";
import { sortOptions } from "@/lib/sortByName";
import { ListField } from "./MailboxGroup";
import type { PostgresForm } from "./postgresForm";
import type { WebhookForm } from "./webhookForm";

// DatabaseGroup is a postgres source's settings (#2408): the application
// database it reads, and whether it files each notification on named
// channels or each row of an outbox table. It takes the place of the
// authentication group an http source has.

type Setter = <K extends keyof WebhookForm>(key: K) => (value: WebhookForm[K]) => void;

export function DatabaseGroup({ form, set }: { form: WebhookForm; set: Setter }) {
  const p = form.postgres;
  const setPG = <K extends keyof PostgresForm>(key: K) => (value: PostgresForm[K]) =>
    set("postgres")({ ...p, [key]: value });
  const connections = useConnections();
  const appdbs = (connections.data?.connections ?? []).filter((c) => c.kind === "appdb");
  return (
    <ConfigGroup title="Database">
      <ConfigSelect
        label="Connection"
        value={p.connection}
        onChange={setPG("connection")}
        options={[
          { value: "", label: connections.isLoading ? "Loading..." : "Choose an application database" },
          ...sortOptions(appdbs.map((c) => ({ value: c.connection, label: c.connection }))),
        ]}
        help="The source reads it as the connection's own role, on a connection of its own outside the pool."
      />
      <ConfigSelect
        label="Mode"
        value={p.mode}
        onChange={(v) => setPG("mode")(v as PostgresForm["mode"])}
        options={[
          { value: "outbox", label: "Outbox" },
          { value: "notify", label: "Notify" },
        ]}
        help={
          p.mode === "outbox"
            ? "Each row of the outbox is filed and deleted in one transaction. A notification wakes the source, and a poll catches any it missed, so no row is lost."
            : "Each notification's payload is one event. A notification sent while no replica listens is lost."
        }
      />
      {p.mode === "notify" ? (
        <ListField
          label="Channels"
          value={p.channels}
          onChange={setPG("channels")}
          placeholder="order_events"
          help="One per line. Sent with NOTIFY or pg_notify."
        />
      ) : (
        <div className="grid gap-3 sm:grid-cols-2">
          <ConfigField
            label="Outbox table"
            required
            mono
            value={p.table}
            onChange={setPG("table")}
            placeholder="public.outbox"
            help="The table manage_schema's outbox template created, as schema.table. The role needs SELECT and DELETE on it."
          />
          <ConfigField
            label="Poll interval (seconds)"
            type="number"
            value={p.pollSeconds}
            onChange={setPG("pollSeconds")}
            placeholder="30"
          />
        </div>
      )}
      <ConfigField
        label="Largest payload (bytes)"
        type="number"
        value={p.maxPayloadBytes}
        onChange={setPG("maxPayloadBytes")}
        placeholder="65536"
        help="A larger notification or row is refused, listed under Rejections, and never stored."
      />
      <p className="text-xs text-muted-foreground">
        The database is checked when the source is saved. One replica listens at a time.
      </p>
    </ConfigGroup>
  );
}
