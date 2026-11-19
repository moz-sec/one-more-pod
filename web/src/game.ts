export type Pod = { id: string; name: string; role: string; cpu: number; memory: number; zone?: string };
export type Node = { id: string; cpu: number; memory: number; zone: string };
export type Recipe = { id: string; name: string; projects: string[]; points: number; description: string };
export type Game = { gameId: string; challenge: string; nodes: Node[]; seed: number; templates: Pod[]; recipes: Recipe[] };
export type Action = { type: 'place' | 'skip' | 'evict'; podId: string; nodeId?: string };
export function streamPod(game: Game, index: number): Pod {
 let x = (game.seed ^ Math.imul(index + 1, 0x9e3779b9)) >>> 0;
 const next = () => { x ^= x << 13; x ^= x >>> 17; x ^= x << 5; return x >>>= 0; };
 const template = game.templates[next() % game.templates.length];
 return { ...template, id: `pod-${index + 1}`, cpu: 1 + next() % 4, memory: 2 + next() % 7 };
}

// The server validates actions; this function calculates the local preview.
export function evaluate(game: Game, actions: Action[]) {
 const placed = new Map<string,string>();
 let progress=0, combo=0, bonus=0, evictions=0, skipped=0, scheduled=0;
 for (const action of actions) {
  if (action.type==='place') {
   placed.set(action.podId, action.nodeId!); progress++; scheduled++; combo++;
   if (combo>1) bonus+=Math.min(combo-1,4)*100;
  } else if (action.type==='skip') { progress++; skipped++; combo=0; }
  else { placed.delete(action.podId); evictions++; combo=0; }
 }
 // Stream IDs are one-based. Only residents contribute to the final layout.
 const allPods=Array.from(placed.keys(), id=>streamPod(game, Number(id.slice(4))-1));
 const loads=game.nodes.map(node=>allPods.filter(p=>placed.get(p.id)===node.id).reduce((v,p)=>({cpu:v.cpu+p.cpu,memory:v.memory+p.memory}),{cpu:0,memory:0}));
 const packed=game.nodes.filter((n,i)=>loads[i].cpu===n.cpu || loads[i].memory===n.memory).length;
 const cpus=loads.map(load=>load.cpu);
 const balanced=cpus.length===3 && cpus.every(cpu=>cpu>0) && Math.max(...cpus)-Math.min(...cpus)<=2;
 const activeProjects=new Set(allPods.filter(p=>placed.has(p.id)).map(p=>p.name));
 const completedRecipes=game.recipes.filter(recipe=>recipe.projects.every(name=>activeProjects.has(name)));
 const serviceBonus=completedRecipes.reduce((sum,recipe)=>sum+recipe.points,0);
 const points=Math.max(0,placed.size*1000+scheduled*300+bonus-evictions*200+packed*300+(balanced?600:0)+serviceBonus);
 return {placed, progress, combo, bonus, evictions, skipped, scheduled, allPods, loads, packed, balanced, activeProjects, completedRecipes, serviceBonus, points};
}
