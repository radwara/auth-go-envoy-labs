CREATE TABLE policies (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    subject varchar(200) NOT NULL CHECK (btrim(subject) <> ''),
    action varchar(200) NOT NULL CHECK (btrim(action) <> ''),
    resource varchar(200) NOT NULL CHECK (btrim(resource) <> ''),
    effect text NOT NULL CHECK (effect IN ('allow', 'deny')),
    UNIQUE (subject, action, resource)
);
