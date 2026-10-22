<script lang="ts">
	import { BaseEdge, getSmoothStepPath, type EdgeProps } from '@xyflow/svelte';

	// An edge out of a Loop or For each (other than its body): it runs straight
	// along the loop's centre line, past the body's box, and turns only near
	// its target. (Svelte Flow doesn't pass pathOptions to edge components.)
	let { sourceX, sourceY, targetX, targetY, sourcePosition, targetPosition, label, labelStyle, style, markerStart, markerEnd, interactionWidth }: EdgeProps = $props();

	let [path, labelX, labelY] = $derived(getSmoothStepPath({ sourceX, sourceY, targetX, targetY, sourcePosition, targetPosition, stepPosition: 1 }));
</script>

<BaseEdge {path} {labelX} {labelY} {label} {labelStyle} {style} {markerStart} {markerEnd} {interactionWidth} />
