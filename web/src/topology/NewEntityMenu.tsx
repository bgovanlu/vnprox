// Topology toolbar's "New" menu: create a bridge/bond/VLAN interface on a
// chosen node (docs/user-guide.md's common-tasks table: "Node -> Bonds ->
// New"). Opens the corresponding entity editor via editorLauncherStore
// (the same launcher the inspector's Edit button uses) with no existing
// target — i.e. create mode.
import * as RadixDropdown from "@radix-ui/react-dropdown-menu";
import { useSession } from "../api/useSession";
import { Button } from "../components/Button";
import { capsForNode } from "../changesets/capabilities";
import { useEditorLauncherStore, type EditorKind } from "../changesets/editorLauncherStore";
import { useMgmtWizardStore } from "../mgmt/mgmtWizardStore";
import { mgmtStrings } from "../mgmt/strings";

export interface NewEntityMenuProps {
  nodes: string[];
}

const KIND_LABEL: Record<EditorKind, string> = {
  bridge: "Bridge",
  "bridge-delete": "Bridge",
  bond: "Bond",
  vlan: "VLAN interface",
  iface: "Interface",
  "iface-rename": "Rename",
};

const CREATABLE_KINDS: EditorKind[] = ["bridge", "bond", "vlan"];

export function NewEntityMenu({ nodes }: NewEntityMenuProps) {
  const { data: session } = useSession();
  const open = useEditorLauncherStore((s) => s.open);
  const openMgmtWizard = useMgmtWizardStore((s) => s.open);
  const writableNodes = nodes.filter((n) => capsForNode(session, n).netWrite);

  if (writableNodes.length === 0) return null;

  return (
    <RadixDropdown.Root>
      <RadixDropdown.Trigger asChild>
        <Button size="sm" variant="secondary">
          New ▾
        </Button>
      </RadixDropdown.Trigger>
      <RadixDropdown.Portal>
        <RadixDropdown.Content
          align="end"
          sideOffset={6}
          className="z-50 max-h-80 min-w-[12rem] overflow-y-auto rounded-md border border-slate-200 bg-white p-1 shadow-lg dark:border-slate-700 dark:bg-slate-900"
        >
          {CREATABLE_KINDS.map((kind) => (
            <RadixDropdown.Sub key={kind}>
              <RadixDropdown.SubTrigger className="cursor-pointer rounded px-2 py-1.5 text-sm outline-none hover:bg-slate-100 dark:hover:bg-slate-800">
                {KIND_LABEL[kind]}
              </RadixDropdown.SubTrigger>
              <RadixDropdown.Portal>
                <RadixDropdown.SubContent className="z-50 min-w-[8rem] rounded-md border border-slate-200 bg-white p-1 shadow-lg dark:border-slate-700 dark:bg-slate-900">
                  {writableNodes.map((node) => (
                    <RadixDropdown.Item
                      key={node}
                      className="cursor-pointer rounded px-2 py-1.5 text-sm outline-none hover:bg-slate-100 dark:hover:bg-slate-800"
                      onSelect={() => {
                        open({ kind, node });
                      }}
                    >
                      {node}
                    </RadixDropdown.Item>
                  ))}
                </RadixDropdown.SubContent>
              </RadixDropdown.Portal>
            </RadixDropdown.Sub>
          ))}
          <RadixDropdown.Separator className="my-1 h-px bg-slate-200 dark:bg-slate-700" />
          <RadixDropdown.Sub>
            <RadixDropdown.SubTrigger className="cursor-pointer rounded px-2 py-1.5 text-sm outline-none hover:bg-slate-100 dark:hover:bg-slate-800">
              {mgmtStrings.launch.button}
            </RadixDropdown.SubTrigger>
            <RadixDropdown.Portal>
              <RadixDropdown.SubContent className="z-50 min-w-[8rem] rounded-md border border-slate-200 bg-white p-1 shadow-lg dark:border-slate-700 dark:bg-slate-900">
                {writableNodes.map((node) => (
                  <RadixDropdown.Item
                    key={node}
                    className="cursor-pointer rounded px-2 py-1.5 text-sm outline-none hover:bg-slate-100 dark:hover:bg-slate-800"
                    onSelect={() => {
                      openMgmtWizard({ node });
                    }}
                  >
                    {node}
                  </RadixDropdown.Item>
                ))}
              </RadixDropdown.SubContent>
            </RadixDropdown.Portal>
          </RadixDropdown.Sub>
        </RadixDropdown.Content>
      </RadixDropdown.Portal>
    </RadixDropdown.Root>
  );
}
