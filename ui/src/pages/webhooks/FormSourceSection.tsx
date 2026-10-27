import type { WebhookFormBinding } from "@/api/admin/types";
import { SectionCard } from "@/components/patterns/SectionCard";

// FormSourceSection is what a form's source collects for (#2178): the form
// whose accepted submissions it stores, one event each, and the personas a
// script may decrypt the form's sensitive fields as (#2179). Nothing posts to
// it; the platform made it when the form was bound to an endpoint.
export function FormSourceSection({ form }: { form: WebhookFormBinding }) {
  const personas = form.decrypt_personas ?? [];
  return (
    <SectionCard title="Form">
      <p className="text-sm text-muted-foreground">
        Each submission the form accepts is stored here as one event. Nothing is posted to this source; the form&apos;s
        binding (manage_script endpoint_set, or the automation&apos;s Endpoint section) is where it is changed.
      </p>
      <dl className="mt-3 grid gap-3 text-xs sm:grid-cols-3">
        <div>
          <dt className="text-muted-foreground">Endpoint</dt>
          <dd className="font-mono">/endpoints/{form.endpoint}</dd>
        </div>
        <div>
          <dt className="text-muted-foreground">Form page</dt>
          <dd className="font-mono">/forms/{form.endpoint}</dd>
        </div>
        <div>
          <dt className="text-muted-foreground">Decrypt personas</dt>
          <dd>{personas.length === 0 ? "None" : personas.join(", ")}</dd>
        </div>
      </dl>
    </SectionCard>
  );
}
