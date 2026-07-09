import { useCallback } from "react";
import { Background, Controls, MiniMap, ReactFlow, type NodeMouseHandler, type OnNodeDrag, type XYPosition } from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import { EntityEdge } from "./EntityEdge";
import { EntityNode } from "./EntityNode";
import type { FlowElements } from "./toFlowElements";

const nodeTypes = { entity: EntityNode };
const edgeTypes = { entity: EntityEdge };

export interface TopologyCanvasProps {
  elements: FlowElements;
  onNodeClick: (id: string) => void;
  onNodeHover: (id: string | undefined) => void;
  onNodeDragStop: (id: string, position: XYPosition) => void;
  onPaneClick: () => void;
}

/**
 * The React Flow canvas itself: pan/zoom, custom node/edge rendering, and
 * the raw pointer events TopologyPage turns into inspector/hover/drag-save
 * behavior. Deliberately thin — all the "what does this click/hover mean"
 * logic lives in projection.ts/TopologyPage, not here, so this component
 * stays a straightforward React Flow wrapper.
 */
export function TopologyCanvas({ elements, onNodeClick, onNodeHover, onNodeDragStop, onPaneClick }: TopologyCanvasProps) {
  const handleNodeClick: NodeMouseHandler = useCallback(
    (_evt, node) => {
      onNodeClick(node.id);
    },
    [onNodeClick],
  );
  const handleMouseEnter: NodeMouseHandler = useCallback(
    (_evt, node) => {
      onNodeHover(node.id);
    },
    [onNodeHover],
  );
  const handleMouseLeave = useCallback(() => {
    onNodeHover(undefined);
  }, [onNodeHover]);
  const handleDragStop: OnNodeDrag = useCallback(
    (_evt, node) => {
      onNodeDragStop(node.id, node.position);
    },
    [onNodeDragStop],
  );

  return (
    <ReactFlow
      nodes={elements.nodes}
      edges={elements.edges}
      nodeTypes={nodeTypes}
      edgeTypes={edgeTypes}
      onNodesChange={() => {
        /* Controlled from TopologyPage's state; position changes are
         * captured via onNodeDragStop instead, so this is intentionally a
         * no-op (its only job is to satisfy React Flow's controlled-nodes
         * contract rather than warn about a missing handler). */
      }}
      onNodeClick={handleNodeClick}
      onNodeMouseEnter={handleMouseEnter}
      onNodeMouseLeave={handleMouseLeave}
      onNodeDragStop={handleDragStop}
      onPaneClick={onPaneClick}
      minZoom={0.05}
      maxZoom={2}
      fitView
      proOptions={{ hideAttribution: true }}
    >
      <Background />
      <Controls />
      <MiniMap pannable zoomable className="!bg-white dark:!bg-slate-900" />
    </ReactFlow>
  );
}
