/** One endpoint an application environment serves, and its URLs there (#2822). */
export interface ApplicationEndpoint {
  name: string;
  script: string;
  methods: string[];
  auth_mode: string;
  /** The path template under the endpoint's name, "" for the name alone. */
  path: string;
  /** Every URL it answers at in this environment, the platform host first. */
  urls: string[];
}
