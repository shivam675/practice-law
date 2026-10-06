-- Permission catalogue. Reference data, versioned with the schema so that a
-- new permission cannot be referenced by code that shipped before it existed.

INSERT INTO permissions (key, resource, action, description, is_platform) VALUES
  ('assessment.create',          'assessment', 'create',          'Create assessments',                       false),
  ('assessment.edit',            'assessment', 'edit',            'Edit assessments',                         false),
  ('assessment.delete',          'assessment', 'delete',          'Delete assessments',                       false),
  ('assessment.publish',         'assessment', 'publish',         'Publish an assessment to participants',    false),
  ('assessment.assign',          'assessment', 'assign',          'Assign teams to an assessment',            false),
  ('assessment.view',            'assessment', 'view',            'View assessment definitions',              false),
  ('assessment.view_own',        'assessment', 'view_own',        'View assessments assigned to oneself',     false),
  ('assessment.grade',           'assessment', 'grade',           'Grade submissions and sessions',           false),
  ('assessment.override_grade',  'assessment', 'override_grade',  'Override an AI-produced grade',            false),

  ('template.create',            'template',   'create',          'Create assessment templates',              false),
  ('template.edit',              'template',   'edit',            'Edit assessment templates',                false),
  ('template.view',              'template',   'view',            'View assessment templates',                false),
  ('template.delete',            'template',   'delete',          'Delete assessment templates',              false),

  ('rubric.create',              'rubric',     'create',          'Create rubrics',                           false),
  ('rubric.edit',                'rubric',     'edit',            'Edit rubrics',                             false),
  ('rubric.view',                'rubric',     'view',            'View rubrics',                             false),

  ('team.create',                'team',       'create',          'Create teams',                             false),
  ('team.edit',                  'team',       'edit',            'Edit team membership',                     false),
  ('team.view',                  'team',       'view',            'View teams',                               false),

  ('session.start',              'session',    'start',           'Start a live session',                     false),
  ('session.join',               'session',    'join',            'Join a live session as a participant',     false),
  ('session.observe',            'session',    'observe',         'Observe a live session without speaking',  false),
  ('session.moderate',           'session',    'moderate',        'Pause, resume or stop a live session',     false),
  ('session.view_transcript',    'session',    'view_transcript', 'View a session transcript',                false),

  ('submission.upload',          'submission', 'upload',          'Upload a submission',                      false),
  ('submission.view',            'submission', 'view',            'View any submission in the organisation',  false),
  ('submission.view_own',        'submission', 'view_own',        'View ones own submissions',                false),
  ('submission.unlock',          'submission', 'unlock',          'Reopen a locked submission',               false),

  ('report.view',                'report',     'view',            'View any report in the organisation',      false),
  ('report.view_own',            'report',     'view_own',        'View ones own reports',                     false),
  ('report.annotate',            'report',     'annotate',        'Add teacher annotations to a report',      false),
  ('report.publish',             'report',     'publish',         'Publish a report to participants',         false),

  ('user.create',                'user',       'create',          'Create users',                             false),
  ('user.edit',                  'user',       'edit',            'Edit users',                               false),
  ('user.view',                  'user',       'view',            'View users',                               false),
  ('user.assign_role',           'user',       'assign_role',     'Grant or revoke roles',                    false),

  ('role.create',                'role',       'create',          'Create custom roles',                      false),
  ('role.edit',                  'role',       'edit',            'Edit custom roles',                        false),
  ('role.view',                  'role',       'view',            'View roles and permissions',               false),

  ('ai_profile.create',          'ai_profile', 'create',          'Create AI actor profiles',                 false),
  ('ai_profile.edit',            'ai_profile', 'edit',            'Edit AI actor profiles',                   false),
  ('ai_profile.view',            'ai_profile', 'view',            'View AI actor profiles',                   false),

  ('knowledge.upload',           'knowledge',  'upload',          'Upload assessment source material',        false),
  ('knowledge.view',             'knowledge',  'view',            'View assessment source material',          false),

  ('audit.view',                 'audit',      'view',            'View the organisation audit log',          false),
  ('usage.view',                 'usage',      'view',            'View model usage and cost',                false),
  ('organization.edit',          'organization','edit',           'Edit organisation settings',               false),

  ('platform.organization.manage', 'platform', 'organization.manage', 'Create and manage organisations', true),
  ('platform.model.configure',     'platform', 'model.configure',     'Configure model providers',       true),
  ('platform.health.view',         'platform', 'health.view',         'View system health',              true)
ON CONFLICT (key) DO NOTHING;
