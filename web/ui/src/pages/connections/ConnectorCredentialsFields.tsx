import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import type { ConnectorField } from "@/lib/connections";

// The credentials field-list renderer shared by ChatAppWizard's "Credentials"
// step (the Connections page slide-over) and the onboarding wizard's inline
// chat-app step (SetupWizard) — both collect the same CredSpec-driven field
// set (e.g. Telegram's single "token", Slack's "token" + "app_token") into a
// { [fieldName]: value } map, they just submit it through different
// endpoints (/api/v1/connectors vs /api/v1/setup).
export function ConnectorCredentialsFields({
  fields,
  values,
  onChange,
}: {
  fields: ConnectorField[];
  values: Record<string, string>;
  onChange: (name: string, value: string) => void;
}) {
  return (
    <div className="space-y-3">
      {fields.map((f) => (
        <div key={f.name} className="space-y-1">
          <Label htmlFor={`field-${f.name}`}>{f.label}</Label>
          <Input
            id={`field-${f.name}`}
            type={f.secret ? "password" : "text"}
            value={values[f.name] ?? ""}
            onChange={(e) => onChange(f.name, e.target.value)}
            autoComplete="off"
          />
        </div>
      ))}
    </div>
  );
}
