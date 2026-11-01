// The task-tool interface. Plane implements it today; the built-in store is the fallback.
// Adding Jira/ClickUp/Linear = one new class implementing this.
import type { Project, State, Member, Label, Cycle, Module, CycleLink, WorkItem, WorkItemWrite } from "../plane.js";

export type ProviderKind = "plane" | "builtin" | "none";

export interface TaskProvider {
  readonly kind: ProviderKind;
  /** Stable id of the data source (stored history is kept per workspace). */
  readonly workspaceKey: string;
  me(): Promise<Member>;
  allProjects(): Promise<Project[]>;
  projects(): Promise<Project[]>;
  states(projectId: string): Promise<State[]>;
  members(projectId: string): Promise<Member[]>;
  labels(projectId: string): Promise<Label[]>;
  cycles(projectId: string): Promise<Cycle[]>;
  modules(projectId: string): Promise<Module[]>;
  cycleLinks(projectId: string, cycleId: string): Promise<CycleLink[]>;
  moduleItemIds(projectId: string, moduleId: string): Promise<string[]>;
  workItems(project: Project): Promise<WorkItem[]>;
  workItemByKey(key: string): Promise<WorkItem>;
  createWorkItem(project: Project, body: WorkItemWrite): Promise<WorkItem>;
  updateWorkItem(project: Project, id: string, body: WorkItemWrite): Promise<WorkItem>;
  addToCycle(projectId: string, cycleId: string, itemIds: string[]): Promise<void>;
  addComment(project: Project, itemId: string, html: string): Promise<void>;
  itemUrl(item: WorkItem): string;
  keyUrl(key: string): string;
  projectUrl(project: Project): string;
}

export type { Project, State, Member, Label, Cycle, Module, CycleLink, WorkItem, WorkItemWrite };
