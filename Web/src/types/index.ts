export interface User {
  uid: number;
  name: string;
  mobile: string; // From API it's masked, but still key is 'mobile' in user response
  avatar: string;
  permission: number;
}

export interface AuthResponse {
  token: string;
  user: User;
}

export interface ApiResponse<T> {
  code: number;
  message: string;
  data: T;
}

export interface WhitelistItem {
  id: number;
  uid: number;
  mobile_masked: string;
  permission: number;
}

export interface Course {
  class_id: number;
  course_id: number;
  name: string;
  teacher: string;
  icon: string;
  is_selected: boolean;
}

export interface SignActivity {
  active_id: number;
  activity_name: string;
  start_time: number;
  end_time: number;
  sign_type: number;
  if_refresh_ewm: boolean;
  record_source_name: string;
  record_source: number;
  record_sign_time: number;
  course_name: string;
  course_id: number;
  class_id: number;
  course_teacher: string;
}

export interface CourseActivities {
  course_id: number;
  class_id: number;
  course_name: string;
  course_teacher: string;
  icon: string;
  has_more: boolean;
  activities: SignActivity[];
}

export interface Classmate {
  uid: number;
  name: string;
  mobile_masked: string;
  avatar: string;
}

export interface SignParams {
  activity_id: number;
  user_ids: number[];
  sign_type: number;
  course_id: number;
  class_id: number;
  if_refresh_ewm: boolean;
  special_params: Record<string, any>;
}

export interface SignStatusMessage {
  type: 'sign_status';
  activity_id: number;
  user_id: number;
  status: 'pending' | 'signing' | 'retrying' | 'success' | 'failed';
  attempt: number;
  message: string;
}

export interface SignCheckItem {
  user_id: number;
  signed: boolean;
  record_source: number;
  record_source_name: string;
  message: string;
}

export interface SignShareCreateResponse {
  token: string;
  expires_at: number;
}

export interface SignShareInfo {
  activity_id: number;
  activity_name: string;
  course_id: number;
  class_id: number;
  course_name: string;
  course_teacher: string;
  sign_type: number;
  if_refresh_ewm: boolean;
  expires_at: number;
}

export interface SignShareExecuteResponse {
  target_count: number;
  success_count: number;
  already_signed_count: number;
  failed_count: number;
  used: boolean;
  message: string;
  failures: string[];
}

export interface AdminAccount {
  uid: number;
  name: string;
  mobile_masked: string;
  avatar: string;
  permission: number;
  last_login_at: number;
  course_count: number;
  selected_count: number;
}

export interface AdminManagedCourse {
  class_id: number;
  course_id: number;
  name: string;
  teacher: string;
  icon: string;
  is_selected: boolean;
}

export interface AdminCreateAccountResponse {
  account: AdminAccount;
  sync_count: number;
  sync_message: string;
}

export interface AdminClassGroup {
  id: number;
  name: string;
  description: string;
  member_count: number;
  member_uids: number[];
}

export type AdminClassGroupSyncMode = 'replace' | 'append';

export interface AdminClassGroupSyncResponse {
  target_count: number;
  course_count: number;
  copied_relations: number;
  mode: AdminClassGroupSyncMode;
}

export interface QMXRoomCheckLocation {
  name: string;
  lng: number;
  lat: number;
  range: number;
  distance?: number;
}

export interface QMXRoomCheckRequirements {
  photo_required: boolean;
  face_required: boolean;
  bluetooth_required: boolean;
  special_sdk: boolean;
}

export interface QMXRoomCheckPreview {
  batch_name: string;
  check_date: string;
  late_date: string;
  start_time: string;
  end_time: string;
  late_end_time: string;
  locations: QMXRoomCheckLocation[];
  requirements: QMXRoomCheckRequirements;
  unsupported: string[];
}

export interface QMXRoomCheckExecuteResponse {
  success: boolean;
  code: number | string;
  message: string;
  batch_name: string;
  check_date: string;
  check_time: string;
  location_name: string;
  longitude: number;
  latitude: number;
  unsupported?: string[];
}

export interface AdminSignRecord {
  id: number;
  user_uid: number;
  user_name: string;
  user_mobile_masked: string;
  source_uid: number;
  source_name: string;
  source_mobile_masked: string;
  activity_id: number;
  activity_name: string;
  course_id: number;
  class_id: number;
  course_name: string;
  course_teacher: string;
  sign_type: number;
  sign_time_ms: number;
}

export interface AdminSignRecordPage {
  items: AdminSignRecord[];
  page: number;
  page_size: number;
  total: number;
  total_pages: number;
}
