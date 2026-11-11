// Who hears about a General feedback post (#2603): what an administrator sets.
export interface GeneralFeedbackSettings {
  personas: string[];
  recipients: string[];
  channels: string[];
  acknowledge_author: boolean;
}

// A persona the list can name, with how many people it reaches now.
export interface GeneralFeedbackPersona {
  name: string;
  display_name: string;
  people: number;
}

// The list as the admin API answers it: the settings, who they reach now and
// what is wrong with them.
export interface GeneralFeedbackView extends GeneralFeedbackSettings {
  people: string[];
  persona_options: GeneralFeedbackPersona[];
  warnings: string[];
  updated_by?: string;
  updated_at?: string;
}
