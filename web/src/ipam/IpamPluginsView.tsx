// SDN IPAM plugin-instance view (T-3104, docs/features/ipam.md): the
// configured IPAM plugin *objects* themselves (netbox/phpipam/pve, their
// connection config) — not the allocations inside them, which stay
// IpamPage.tsx's own subnet/address-list views. Mirrors ControllersView.tsx
// (T-3102) field-for-field: list with type badge, a create/edit form whose
// fields reveal per the selected type, deletion blocked (server-side, this
// task's own acceptance criterion 2 analogue of T-3102's criterion 5) while
// a zone still references the instance. Data comes from GET /sdn's
// SdnTree.ipams (internal/sdn.Service.buildIpams), the same tree
// Fabrics/Controllers read from — not a separate /ipam/* route, since a
// plugin instance's *configuration* is SDN-object state, unlike the
// allocations inside it.
//
// `token` never appears anywhere in this file's read path: SdnIpam
// (api/types.ts) carries no token field at all, because real PVE never
// echoes a configured secret back on a read (internal/pve/sdn_ipam.go's
// package doc comment). Editing an existing netbox/phpipam instance always
// starts with an empty token field; leaving it empty on save keeps
// whatever PVE already has stored (buildSdnIpamUpdateOp only sends `token`
// when the operator retyped it).
import { useState } from "react";
import { useSession } from "../api/useSession";
import { Button } from "../components/Button";
import { EmptyState } from "../components/EmptyState";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "../components/Table";
import { HelpAnchor } from "../help/HelpAnchor";
import type { SdnIpam } from "../api/types";
import { hasAnyCap, missingCapTooltip } from "../changesets/capabilities";
import {
  buildSdnIpamCreateOp,
  buildSdnIpamDeleteOp,
  buildSdnIpamUpdateOp,
  type SdnIpamFormValues,
} from "../changesets/opBuilders";
import { Field, inputClass } from "../changesets/editors/EditorDialog";
import { EditorDialog } from "../changesets/editors/EditorDialog";
import { useEditorSubmit } from "../changesets/editors/useEditorSubmit";
import { SdnConfirmDeleteDialog } from "../sdn/editors/SdnConfirmDeleteDialog";
import { useSdnQuery } from "../sdn/queries";

const IPAM_TYPES = [
  { value: "pve", label: "PVE (built-in)" },
  { value: "netbox", label: "NetBox" },
  { value: "phpipam", label: "phpIPAM" },
];

function typeLabel(type: string): string {
  return IPAM_TYPES.find((t) => t.value === type)?.label ?? type;
}

function emptyIpamForm(): SdnIpamFormValues {
  return { type: "pve", url: "", token: "", fingerprint: "", section: 0 };
}

/** ipam -> form. `token` is always "" here (SdnIpam carries none) — see
 * this file's own doc comment. */
function ipamToForm(ipam: SdnIpam): SdnIpamFormValues {
  return {
    type: ipam.type,
    url: ipam.url ?? "",
    token: "",
    fingerprint: ipam.fingerprint ?? "",
    section: ipam.section ?? 0,
  };
}

interface IpamFormDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** Existing instance — undefined for create. */
  existing?: SdnIpam;
}

function IpamFormDialog({ open, onOpenChange, existing }: IpamFormDialogProps) {
  const { data: session } = useSession();
  const { findings, submit } = useEditorSubmit();

  const initial = existing ? ipamToForm(existing) : emptyIpamForm();
  const [ipamId, setIpamId] = useState("");
  const [type, setType] = useState(initial.type);
  const [url, setUrl] = useState(initial.url);
  const [token, setToken] = useState("");
  const [fingerprint, setFingerprint] = useState(initial.fingerprint);
  const [section, setSection] = useState(initial.section);

  const isCreate = !existing;
  const isExternal = type === "netbox" || type === "phpipam";
  const target = existing ? `sdn-ipam::${existing.id}` : `sdn-ipam::${ipamId}`;
  const disabledReason = missingCapTooltip(session, "", "sdnWrite");

  function handleSubmit(): void {
    const form: SdnIpamFormValues = { type, url, token, fingerprint, section };
    const op = isCreate ? buildSdnIpamCreateOp(target, form) : buildSdnIpamUpdateOp(target, initial, form);
    const label = isCreate ? ipamId : existing.id;
    submit([op], `Edit sdn ipam ${label}`, target, () => {
      onOpenChange(false);
    });
  }

  return (
    <EditorDialog
      open={open}
      onOpenChange={onOpenChange}
      title={isCreate ? "Create IPAM plugin instance" : `Edit IPAM plugin ${existing.id}`}
      description="netbox/phpipam connect vnprox's IPAM view to an external system; pve is Proxmox's own built-in plugin and needs no connection config."
      onSubmit={handleSubmit}
      disabledReason={!hasAnyCap(session, "sdnWrite") ? disabledReason : undefined}
      generalErrors={findings.general}
    >
      {isCreate && (
        <Field label="ID" help="Starts and ends with a letter or digit; letters and digits only in between (no hyphens/underscores).">
          <input className={inputClass} value={ipamId} onChange={(e) => { setIpamId(e.target.value); }} placeholder="netbox1" />
        </Field>
      )}

      {isCreate && (
        <Field label="Type" help="Not editable after creation — changing type is a delete+create.">
          <select className={inputClass} value={type} onChange={(e) => { setType(e.target.value); }}>
            {IPAM_TYPES.map((t) => (
              <option key={t.value} value={t.value}>
                {t.label}
              </option>
            ))}
          </select>
        </Field>
      )}

      {isExternal && (
        <>
          <Field label="URL" errors={findings.byField.url} help="The external system's API base URL.">
            <input className={inputClass} value={url} onChange={(e) => { setUrl(e.target.value); }} placeholder="https://netbox.example.com" />
          </Field>
          <Field
            label="Token"
            errors={findings.byField.token}
            help={
              existing
                ? "Write-only — PVE never reports the stored token back, so this field always starts empty. Leave it blank to keep the existing token unchanged; retype it only to replace it."
                : "API token PVE uses to authenticate to the external system."
            }
          >
            <input type="password" className={inputClass} value={token} onChange={(e) => { setToken(e.target.value); }} placeholder={existing ? "(unchanged)" : ""} />
          </Field>
          <Field label="Fingerprint" errors={findings.byField.fingerprint} help="Optional certificate SHA-256 fingerprint, for a self-signed HTTPS endpoint.">
            <input className={inputClass} value={fingerprint} onChange={(e) => { setFingerprint(e.target.value); }} placeholder="AA:BB:...:FF" />
          </Field>
          <Field label="Section" errors={findings.byField.section} help="Optional phpIPAM section id.">
            <input type="number" className={inputClass} value={section} onChange={(e) => { setSection(Number(e.target.value)); }} />
          </Field>
        </>
      )}
    </EditorDialog>
  );
}

function IpamRow({
  ipam,
  gateDisabled,
  gateTitle,
  onEdit,
  onDelete,
}: {
  ipam: SdnIpam;
  gateDisabled: boolean;
  gateTitle: string | undefined;
  onEdit: () => void;
  onDelete: () => void;
}) {
  return (
    <TableRow>
      <TableCell className="font-medium">{ipam.id}</TableCell>
      <TableCell>{typeLabel(ipam.type)}</TableCell>
      <TableCell>{ipam.pending ?? "—"}</TableCell>
      <TableCell className="font-mono text-xs">{ipam.url ?? "—"}</TableCell>
      <TableCell>
        <div className="flex justify-end gap-1.5">
          <Button variant="secondary" size="sm" disabled={gateDisabled} title={gateTitle} onClick={onEdit}>
            Edit
          </Button>
          <Button variant="secondary" size="sm" disabled={gateDisabled} title={gateTitle} onClick={onDelete}>
            Delete
          </Button>
        </div>
      </TableCell>
    </TableRow>
  );
}

function IpamsTable({
  ipams,
  gateDisabled,
  gateTitle,
  onEdit,
  onDelete,
}: {
  ipams: SdnIpam[];
  gateDisabled: boolean;
  gateTitle: string | undefined;
  onEdit: (ipam: SdnIpam) => void;
  onDelete: (ipam: SdnIpam) => void;
}) {
  if (ipams.length === 0) {
    return (
      <EmptyState
        title="No IPAM plugin instances configured"
        description="An instance is a netbox/phpipam/pve plugin object a zone can reference by id via its own ipam field. Create one to get started."
      />
    );
  }
  return (
    <Table aria-label="SDN IPAM plugin instances">
      <TableHeader>
        <TableRow>
          <TableHead>ID</TableHead>
          <TableHead>Type</TableHead>
          <TableHead>Pending</TableHead>
          <TableHead>URL</TableHead>
          <TableHead />
        </TableRow>
      </TableHeader>
      <TableBody>
        {ipams.map((i) => (
          <IpamRow
            key={i.id}
            ipam={i}
            gateDisabled={gateDisabled}
            gateTitle={gateTitle}
            onEdit={() => { onEdit(i); }}
            onDelete={() => { onDelete(i); }}
          />
        ))}
      </TableBody>
    </Table>
  );
}

export function IpamPluginsView() {
  const { data: session } = useSession();
  const { data: tree, isLoading, isError } = useSdnQuery();
  const [createOpen, setCreateOpen] = useState(false);
  const [editingIpam, setEditingIpam] = useState<SdnIpam | undefined>(undefined);
  const [deletingIpam, setDeletingIpam] = useState<SdnIpam | undefined>(undefined);

  const canWrite = hasAnyCap(session, "sdnWrite");
  const gateTitle = canWrite ? undefined : missingCapTooltip(session, "", "sdnWrite");

  if (isLoading) {
    return <p className="text-sm text-slate-400">Loading IPAM plugin instances…</p>;
  }
  if (isError || !tree) {
    return (
      <EmptyState
        title="Could not load IPAM plugin instances"
        description="Check that vnproxd can reach the local PVE API, then reload."
      />
    );
  }

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center justify-between gap-2">
        <h2 className="flex items-center gap-1.5 text-base font-semibold">
          IPAM plugins
          <HelpAnchor topic="sdn-ipams" />
        </h2>
        <Button
          variant="primary"
          size="sm"
          disabled={!canWrite}
          title={gateTitle}
          onClick={() => { setCreateOpen(true); }}
        >
          + New IPAM plugin
        </Button>
      </div>

      <IpamsTable
        ipams={tree.ipams}
        gateDisabled={!canWrite}
        gateTitle={gateTitle}
        onEdit={setEditingIpam}
        onDelete={setDeletingIpam}
      />

      {createOpen && <IpamFormDialog open={createOpen} onOpenChange={setCreateOpen} />}
      {editingIpam && (
        <IpamFormDialog
          open={!!editingIpam}
          onOpenChange={(open) => { if (!open) setEditingIpam(undefined); }}
          existing={editingIpam}
        />
      )}
      {deletingIpam && (
        <SdnConfirmDeleteDialog
          open={!!deletingIpam}
          onOpenChange={(open) => { if (!open) setDeletingIpam(undefined); }}
          title={`Delete IPAM plugin ${deletingIpam.id}?`}
          description="Blocked if any zone still references this instance. This adds a delete op to the current changeset draft."
          op={buildSdnIpamDeleteOp(`sdn-ipam::${deletingIpam.id}`)}
          changesetTitle={`Delete sdn ipam ${deletingIpam.id}`}
        />
      )}
    </div>
  );
}
