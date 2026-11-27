export interface ISelfServiceCategory {
  id: number;
  name: string;
  team_id: number;
  created_at: string;
  updated_at: string;
}

export interface ICreateSelfServiceCategoryFormData {
  team_id: number;
  name: string;
}

export interface IEditSelfServiceCategoryFormData {
  name: string;
}
