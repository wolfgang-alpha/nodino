CREATE DATABASE IF NOT EXISTS nodino;
USE nodino;

CREATE TABLE conversations (
    id INT AUTO_INCREMENT PRIMARY KEY,
    started_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    ended_at TIMESTAMP NULL,
    summary VARCHAR(500) NULL
);

CREATE TABLE knots (
    id INT AUTO_INCREMENT PRIMARY KEY,
    conversation_id INT,
    content TEXT NOT NULL,
    type ENUM(
        'event',
        'appointment',
        'reminder',
        'observation',
        'mood',
        'log',
        'anecdote',
        'idea',
        'project',
        'decision',
        'contact',
        'task'
    ) NOT NULL,
    importance TINYINT NOT NULL DEFAULT 3,
    status ENUM('backlog', 'todo', 'in_progress', 'done') NULL,
    occurs_at DATETIME NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    FOREIGN KEY (conversation_id) REFERENCES conversations(id)
);

CREATE TABLE entities (
    id INT AUTO_INCREMENT PRIMARY KEY,
    name VARCHAR(200) NOT NULL,
    kind ENUM('person', 'animal', 'place', 'organization', 'thing') NOT NULL,
    description TEXT,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
);

CREATE TABLE knot_entities (
    knot_id INT NOT NULL,
    entity_id INT NOT NULL,
    role VARCHAR(100),
    PRIMARY KEY (knot_id, entity_id),
    FOREIGN KEY (knot_id) REFERENCES knots(id),
    FOREIGN KEY (entity_id) REFERENCES entities(id)
);

CREATE TABLE nodinos (
    id INT AUTO_INCREMENT PRIMARY KEY,
    knot_a_id INT NOT NULL,
    knot_b_id INT NOT NULL,
    relationship ENUM('same_thread', 'follow_up', 'caused_by', 'related') NOT NULL DEFAULT 'related',
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (knot_a_id) REFERENCES knots(id),
    FOREIGN KEY (knot_b_id) REFERENCES knots(id)
);
