package application

type ValidationError struct {
	Message string
	Fields  map[string]string
}

func (e *ValidationError) Error() string { return e.Message }

func NewValidationError(message string, fields map[string]string) error {
	return &ValidationError{Message: message, Fields: fields}
}
